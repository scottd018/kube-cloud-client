package client

import (
	"context"
	"encoding/base64"
	"fmt"

	awsv2 "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	awsv1 "github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/sts"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

const (
	stsClusterHeader = "x-k8s-aws-id"
	stsPresignPrefix = "k8s-aws-v1."
)

// eksConfig represents a configuration needed to create an EKS kubernetes dynamic client.
type eksConfig struct {
	context       context.Context
	clusterName   string
	clusterClient *eks.Client

	configv1 *awsv1.Config
	configV2 *awsv2.Config
}

// NewEKSConfig creates a new instance of an EKS client config givent the necessary inputs.
func NewEKSConfig(clusterName string) (*eksConfig, error) {
	ctx := context.Background()

	// create a new v2 version of the configuration
	cfgv2, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("error creating EKSConfig for cluster [%s] - %w", clusterName, err)
	}

	// ensure we have the region
	if cfgv2.Region == "" {
		return nil, fmt.Errorf("missing region from config")
	}

	// create a new v1 version of the configuration
	cfgv1 := &awsv1.Config{Region: &cfgv2.Region}

	// return the eks config
	return &eksConfig{
		context:       ctx,
		clusterName:   clusterName,
		clusterClient: eks.NewFromConfig(cfgv2),
		configv1:      cfgv1,
		configV2:      &cfgv2,
	}, nil
}

// NewForKubernetes returns a Kubernetes dynamic.Interface for a given
// EKS cluster config.  It uses the underlying system configuration for the AWS SDK
// in order to properly construct the client.
func (cfg *eksConfig) NewForKubernetes() (dynamic.Interface, error) {
	// create the sts client used for generating a token for authenticating with eks
	stsSession, err := session.NewSession(cfg.configv1)
	if err != nil {
		return nil, fmt.Errorf("unable to create new sts session - %w", err)
	}

	stsClient := sts.New(stsSession)

	// retrieve the token
	request, _ := stsClient.GetCallerIdentityRequest(&sts.GetCallerIdentityInput{})
	request.HTTPRequest.Header.Add(stsClusterHeader, cfg.clusterName)

	presignedURLString, err := request.Presign(60)
	if err != nil {
		return nil, fmt.Errorf("error pre-signing request - %w", err)
	}

	// retrieve the cluster info from the eks service
	clusterInfo, err := cfg.clusterClient.DescribeCluster(cfg.context, &eks.DescribeClusterInput{Name: &cfg.clusterName})
	if err != nil {
		return nil, fmt.Errorf("error describing cluster: [%s] - %w", cfg.clusterName, err)
	}

	// retrieve the cluster certificate
	cert, err := base64.StdEncoding.DecodeString(*clusterInfo.Cluster.CertificateAuthority.Data)
	if err != nil {
		return nil, fmt.Errorf("error retrieving certificate authority data from cluster [%s] - %w", cfg.clusterName, err)
	}

	// create the kubernetes dynamic client
	clientset, err := dynamic.NewForConfig(
		&rest.Config{
			Host:        *clusterInfo.Cluster.Endpoint,
			BearerToken: stsPresignPrefix + base64.RawURLEncoding.EncodeToString([]byte(presignedURLString)),
			TLSClientConfig: rest.TLSClientConfig{
				CAData: cert,
			},
		},
	)

	if err != nil {
		return nil, fmt.Errorf("error creating kubeconfig client - %w", err)
	}

	return clientset, nil
}
