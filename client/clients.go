package client

import (
	"fmt"
	"sync"

	"github.com/rancher/norman/clientbase"
	clusterv1 "github.com/rancher/types/apis/cluster.cattle.io/v1"
	"github.com/sirupsen/logrus"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

type Client struct {
	sync.Mutex
	restClient                 rest.Interface
	componentstatusControllers map[string]ComponentStatusController
	nodeControllers            map[string]NodeController
}

type Clients struct {
	// ClusterClientV1 is the client to connect to Kubernetes cluster API
	ClusterClientV1 *Client
	// ClusterControllerClientV1 is the client for connecting to a cluster controller
	ClusterControllerClientV1 clusterv1.Interface
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
	kubernetesClient := &Client{
		restClient:                 rest,
		nodeControllers:            map[string]NodeController{},
		componentstatusControllers: map[string]ComponentStatusController{},
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

	clientSet := &Clients{kubernetesClient, clusterManagerClient}
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

func (c *Client) ComponentStatuses(namespace string) ComponentStatusInterface {
	objectClient := clientbase.NewObjectClient(namespace, c.restClient, &CSResource, CSGroupVersionKind, componentstatusFactory{})
	return &componentstatusClient{
		ns:           namespace,
		client:       c,
		objectClient: objectClient,
	}
}
