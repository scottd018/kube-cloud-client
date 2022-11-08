package client

import (
	"context"
	"encoding/base64"
	"fmt"

	// 	"encoding/base64"
	// 	"fmt"
	// 	"strings"
	container "google.golang.org/api/container/v1"
	"k8s.io/client-go/dynamic"

	// 	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"
	// 	"sigs.k8s.io/controller-runtime/pkg/client"
	// 	kbresource "sigs.k8s.io/kubebuilder/v3/pkg/model/resource"
)

const (
	gkeAuthScope = "https://www.googleapis.com/auth/cloud-platform"
)

// gkeConfig represents a configuration needed to create a GKE kubernetes dynamic client.
type gkeConfig struct {
	context       context.Context
	clusterName   string
	project       string
	zone          string
	clusterClient *container.Service
}

// NewGKEConfig creates a new instance of an GKE client config givent the necessary inputs.
func NewGKEConfig(clusterName, project, zone string) (*gkeConfig, error) {
	ctx := context.Background()

	// create the gke container service client from the environment
	clusterClient, err := container.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("error creating container cluster client - %w", err)
	}

	// return the gke config
	return &gkeConfig{
		context:       ctx,
		clusterName:   clusterName,
		project:       project,
		zone:          zone,
		clusterClient: clusterClient,
	}, nil
}

// NewForKubernetes returns a Kubernetes dynamic.Interface for a given
// EKS cluster config.  It uses the underlying system configuration for the AWS SDK
// in order to properly construct the client.
func (cfg *gkeConfig) NewForKubernetes() (dynamic.Interface, error) {
	// create a basic kubeconfig structure
	kubeconfig := api.Config{
		APIVersion: "v1",
		Kind:       "Config",
		Clusters:   map[string]*api.Cluster{},  // Clusters is a map of referencable names to cluster configs
		AuthInfos:  map[string]*api.AuthInfo{}, // AuthInfos is a map of referencable names to user configs
		Contexts:   map[string]*api.Context{},  // Contexts is a map of referencable names to context configs
	}

	// get cluster objects
	// TODO: we should be able to filter and find a single object but this works for now.
	response, err := cfg.clusterClient.Projects.Zones.Clusters.List(cfg.project, "-").Context(cfg.context).Do()
	if err != nil {
		return nil, fmt.Errorf(
			"error getting container cluster [%s] in project [%s] and zone [%s] - %w",
			cfg.clusterName,
			cfg.project,
			cfg.zone,
			err,
		)
	}

	// configure kubeconfig
	for _, cluster := range response.Clusters {
		if cluster.Name != cfg.clusterName {
			continue
		}

		cert, err := base64.StdEncoding.DecodeString(cluster.MasterAuth.ClusterCaCertificate)
		if err != nil {
			return nil, fmt.Errorf(
				"invalid certificate cluster [%s] cert [%s] - %w",
				cfg.clusterName,
				cluster.MasterAuth.ClusterCaCertificate,
				err,
			)
		}

		kubeconfig.Clusters[cfg.clusterName] = &api.Cluster{
			CertificateAuthorityData: cert,
			Server:                   "https://" + cluster.Endpoint,
		}

		kubeconfig.Contexts[cfg.clusterName] = &api.Context{
			Cluster:  cfg.clusterName,
			AuthInfo: cfg.clusterName,
		}

		kubeconfig.AuthInfos[cfg.clusterName] = &api.AuthInfo{
			AuthProvider: &api.AuthProviderConfig{
				Name: "gcp",
				Config: map[string]string{
					"scopes": gkeAuthScope,
				},
			},
		}

		clientConfig, err := clientcmd.NewNonInteractiveClientConfig(
			kubeconfig,
			cfg.clusterName,
			&clientcmd.ConfigOverrides{CurrentContext: cfg.clusterName},
			nil,
		).ClientConfig()

		if err != nil {
			return nil, fmt.Errorf("%w - failed to create Kubernetes configuration for cluster [%s]", err, cfg.clusterName)
		}

		return dynamic.NewForConfig(clientConfig)
	}

	return nil, fmt.Errorf(
		"error finding container cluster [%s] in project [%s] and zone [%s] - %w",
		cfg.clusterName,
		cfg.project,
		cfg.zone,
		err,
	)
}
