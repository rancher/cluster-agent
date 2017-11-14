package nodesyncer

import (
	"context"

	client "github.com/rancher/cluster-agent/client"
	"github.com/rancher/cluster-agent/controller"
	"k8s.io/api/core/v1"
)

type NodeSyncer struct {
	client     *client.V1
	controller client.NodeController
}

func init() {
	n := &NodeSyncer{}
	controller.RegisterController(n.GetName(), n)
}

func (n *NodeSyncer) GetName() string {
	return "nodeSyncer"
}

func (n *NodeSyncer) Run(client *client.V1, ctx context.Context) error {
	n.controller = client.ClusterClientV1.Nodes("").Controller()
	n.controller.AddHandler(n.sync)
	n.controller.Start(1, ctx)
	return nil
}

func (n *NodeSyncer) sync(key string, node *v1.Node) error {
	if node == nil {
		// TODO remove the node
		return nil
	} else {
		// TODo update the node
	}
	return nil
}
