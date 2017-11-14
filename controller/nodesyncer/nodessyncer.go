package nodesyncer

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"

	client "github.com/rancher/cluster-agent/client"
	"github.com/rancher/cluster-agent/controller"
	clusterv1 "github.com/rancher/types/apis/cluster.cattle.io/v1"
	"k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type NodeSyncer struct {
	client      *client.V1
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

func (n *NodeSyncer) Run(clusterName string, client *client.V1, ctx context.Context) error {
	n.clusterName = clusterName
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

func (m *NodeSyncer) deleteClusterNode(nodeName string) error {
	clusterNode, err := m.getClusterNode(nodeName)
	if err != nil {
		return err
	}
	logrus.Infof("Deleting cluster node [%s]", clusterNode.Name)

	if clusterNode == nil {
		logrus.Infof("ClusterNode [%s] is already deleted")
		return nil
	}
	err = m.client.ClusterControllerClientV1.ClusterNodes("").Delete(clusterNode.ObjectMeta.Name, nil)
	if err != nil {
		return fmt.Errorf("Failed to delete cluster node [%s] %v", clusterNode.Name, err)
	}
	logrus.Infof("Deleted cluster node [%s]", clusterNode.Name)
	return nil
}

func (m *NodeSyncer) getClusterNode(nodeName string) (*clusterv1.ClusterNode, error) {
	clusterNodeName := fmt.Sprintf("%s-%s", m.clusterName, nodeName)
	logrus.Infof("Getting cluster node [%s]", clusterNodeName)
	existing, err := m.client.ClusterControllerClientV1.ClusterNodes("").Get(clusterNodeName, metav1.GetOptions{})
	logrus.Infof("Got cluster node [%s]", clusterNodeName)

	if err != nil && !apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("Failed to get cluster node by name [%s] %v", clusterNodeName, err)
	}
	return existing, nil
}

func (m *NodeSyncer) createOrUpdateClusterNode(node *v1.Node) error {
	existing, err := m.getClusterNode(node.Name)
	if err != nil {
		return err
	}
	clusterNode := m.convertNodeToClusterNode(node)
	if existing == nil {
		logrus.Infof("Creating cluster node [%s]", clusterNode.Name)
		_, err := m.client.ClusterControllerClientV1.ClusterNodes("").Create(clusterNode)
		if err != nil {
			return fmt.Errorf("Failed to create cluster node [%s] %v", clusterNode.Name, err)
		}
	} else {
		logrus.Infof("Updating cluster node [%s]", clusterNode.Name)
		//TODO - consider doing merge2ways once more than one controller modifies the clusterNode
		_, err := m.client.ClusterControllerClientV1.ClusterNodes("").Update(clusterNode)
		if err != nil {
			return fmt.Errorf("Failed to update cluster node [%s] %v", clusterNode.Name, err)
		}
	}
	return nil
}

func (m *NodeSyncer) convertNodeToClusterNode(node *v1.Node) *clusterv1.ClusterNode {
	clusterNode := &clusterv1.ClusterNode{
		Node: *node,
	}
	clusterNode.APIVersion = ""
	clusterNode.Kind = ""
	clusterNode.ObjectMeta = metav1.ObjectMeta{
		Name:        fmt.Sprintf("%s-%s", m.clusterName, node.Name),
		Labels:      node.Labels,
		Annotations: node.Annotations,
	}
	return clusterNode
}
