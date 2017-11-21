package client

import (
	"fmt"
	"sync"

	"github.com/rancher/norman/clientbase"
	authzv1 "github.com/rancher/types/apis/authorization.cattle.io/v1"
	clusterv1 "github.com/rancher/types/apis/cluster.cattle.io/v1"
	"github.com/sirupsen/logrus"
	"k8s.io/client-go/dynamic"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

type Client struct {
	sync.Mutex
	restClient rest.Interface
	Clientset  clientset.Interface

	nodeControllers map[string]NodeController
}

type Clients struct {
	// ClusterClientV1 is the client to connect to Kubernetes cluster API
	ClusterClientV1 *Client
	// ClusterControllerClientV1 is the client for connecting to a cluster controller
	ClusterControllerClientV1 clusterv1.Interface

	AuthorizationClientV1 authzv1.Interface
}

func NewClientSetV1(clusterManagerCfg string, clusterCfg string) (*Clients, error) {
	// build kubernetes config
	var kubeConfig *rest.Config
	var err error
	if clusterCfg != "" {
		logrus.Info("Using out of cluster config to connect to kubernetes cluster")
		kubeConfig, err = clientcmd.BuildConfigFromFlags("", clusterCfg)
	} else {
		logrus.Info("Using in cluster config to connect to kubernetes cluster")
		kubeConfig, err = rest.InClusterConfig()
	}
	if err != nil {
		return nil, fmt.Errorf("Failed to build cluster config: %v", err)
	}

	if kubeConfig.NegotiatedSerializer == nil {
		configConfig := dynamic.ContentConfig()
		kubeConfig.NegotiatedSerializer = configConfig.NegotiatedSerializer
	}
	rest, err := rest.UnversionedRESTClientFor(kubeConfig)
	if err != nil {
		return nil, fmt.Errorf("Failed to build cluster client: %v", err)
	}

	clusterClient, err := clientset.NewForConfig(kubeConfig)
	if err != nil {
		return nil, fmt.Errorf("Failed to build cluster client: %v", err)
	}

	kubernetesClient := &Client{
		restClient:      rest,
		Clientset:       clusterClient,
		nodeControllers: map[string]NodeController{},
	}

	// build rancher config
	clusterManagerCfgConfig, err := clientcmd.BuildConfigFromFlags("", clusterManagerCfg)
	if err != nil {
		return nil, fmt.Errorf("Failed to build cluster manager config: %v", err)
	}
	clusterManagerClient, err := clusterv1.NewForConfig(*clusterManagerCfgConfig)
	if err != nil {
		return nil, fmt.Errorf("Failed to build cluster manager client: %v", err)
	}
	authzClient, err := authzv1.NewForConfig(*clusterManagerCfgConfig)
	if err != nil {
		return nil, fmt.Errorf("Failed to build authz client: %v", err)
	}

	clientSet := &Clients{kubernetesClient, clusterManagerClient, authzClient}
	return clientSet, nil
}

func (c *Client) Nodes(namespace string) NodeInterface {
	objectClient := clientbase.NewObjectClient(namespace, c.restClient, &NodeResource, NodeGroupVersionKind, nodeFactory{})
	return &nodeClient{
		ns:           namespace,
		client:       c,
		objectClient: objectClient,
	}
}
