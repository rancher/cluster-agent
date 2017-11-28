package nodesyncer

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"

	client "github.com/rancher/cluster-agent/client"
	"github.com/rancher/cluster-agent/controller"
	clusterv1 "github.com/rancher/types/apis/cluster.cattle.io/v1"
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type NodeSyncer struct {
	client      *client.Clients
	controller  client.NodeController
	clusterName string
}

func init() {
	n := &NodeSyncer{}
	controller.RegisterController(n.GetName(), n)
}

func (n *NodeSyncer) GetName() string {
	return "nodeSyncer"
}

func (n *NodeSyncer) Run(ctx context.Context, clusterName string, client *client.Clients) error {
	n.clusterName = clusterName
	n.client = client
	n.controller = client.ClusterClientV1.Nodes("").Controller()
	n.controller.AddHandler(n.sync)
	n.controller.Start(1, ctx)
	return nil
}

func (n *NodeSyncer) sync(key string, node *v1.Node) error {
	if node == nil {
		return n.deleteClusterNode(key)
	}
	return n.createOrUpdateClusterNode(node)
}

func (n *NodeSyncer) deleteClusterNode(nodeName string) error {
	clusterNode, err := n.getClusterNode(nodeName)
	if err != nil {
		return err
	}
	logrus.Infof("Deleting cluster node [%s]", clusterNode.Name)

	if clusterNode == nil {
		logrus.Infof("ClusterNode [%s] is already deleted")
		return nil
	}
	err = n.client.ClusterControllerClientV1.ClusterNodes("").Delete(clusterNode.ObjectMeta.Name, nil)
	if err != nil {
		return fmt.Errorf("Failed to delete cluster node [%s] %v", clusterNode.Name, err)
	}
	logrus.Infof("Deleted cluster node [%s]", clusterNode.Name)
	return nil
}

func (n *NodeSyncer) getClusterNode(nodeName string) (*clusterv1.ClusterNode, error) {
	clusterNodeName := fmt.Sprintf("%s-%s", n.clusterName, nodeName)
	existing, _ := n.client.ClusterControllerClientV1.ClusterNodes("").Get(clusterNodeName, metav1.GetOptions{})
	//FIXME - add not found error validation once fixed on norman side
	// if err != nil && !apierrors.IsNotFound(err) {
	// 	return nil, fmt.Errorf("Failed to get cluster node by name [%s] %v", clusterNodeName, err)
	// }

	if existing.Name == "" {
		return nil, nil
	}

	return existing, nil
}

func (n *NodeSyncer) createOrUpdateClusterNode(node *v1.Node) error {
	existing, err := n.getClusterNode(node.Name)
	if err != nil {
		return err
	}
	clusterNode := n.convertNodeToClusterNode(node)
	cluster, err := n.client.ClusterControllerClientV1.Clusters("").Get(clusterNode.ClusterName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("Failed to get cluster [%s] %v", clusterNode.ClusterName, err)
	}
	if cluster.ObjectMeta.DeletionTimestamp != nil {
		return fmt.Errorf("Cluster [%s] in removing state", cluster.Name)
	}
	if existing == nil {
		logrus.Infof("Creating cluster node [%s]", clusterNode.Name)
		clusterNode.ObjectMeta.OwnerReferences = append(clusterNode.ObjectMeta.OwnerReferences, metav1.OwnerReference{Name: n.clusterName})
		_, err := n.client.ClusterControllerClientV1.ClusterNodes("").Create(clusterNode)
		if err != nil {
			return fmt.Errorf("Failed to create cluster node [%s] %v", clusterNode.Name, err)
		}
		logrus.Infof("Created cluster node [%s]", clusterNode.Name)
	} else {
		logrus.Infof("Updating cluster node [%s]", clusterNode.Name)
		//TODO - consider doing merge2ways once more than one controller modifies the clusterNode
		clusterNode.ObjectMeta.ResourceVersion = existing.ObjectMeta.ResourceVersion
		_, err := n.client.ClusterControllerClientV1.ClusterNodes("").Update(clusterNode)
		if err != nil {
			return fmt.Errorf("Failed to update cluster node [%s] %v", clusterNode.Name, err)
		}
		logrus.Infof("Updated cluster node [%s]", clusterNode.Name)
	}
	return nil
}

func (n *NodeSyncer) convertNodeToClusterNode(node *v1.Node) *clusterv1.ClusterNode {
	if node == nil {
		return nil
	}
	clusterNode := &clusterv1.ClusterNode{
		Node: *node,
	}
	clusterNode.APIVersion = "cluster.cattle.io/v1"
	clusterNode.Kind = "ClusterNode"
	clusterNode.ObjectMeta = metav1.ObjectMeta{
		Name:        fmt.Sprintf("%s-%s", n.clusterName, node.Name),
		Labels:      node.Labels,
		Annotations: node.Annotations,
	}
	return clusterNode
}
