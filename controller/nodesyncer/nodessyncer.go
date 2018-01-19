package nodesyncer

import (
	"fmt"
	"reflect"

	"github.com/pkg/errors"
	"github.com/rancher/types/apis/core/v1"
	"github.com/rancher/types/apis/management.cattle.io/v3"
	"github.com/rancher/types/config"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

const (
	allMachineKey = "_machine_all_"
)

type NodeSyncer struct {
	machinesClient   v3.MachineInterface
	clusterNamespace string
}

type MachinesSyncer struct {
	machinesClient   v3.MachineInterface
	machines         v3.MachineLister
	nodes            v1.NodeLister
	clusters         v3.ClusterLister
	clusterNamespace string
}

func Register(cluster *config.ClusterContext) {
	n := &NodeSyncer{
		clusterNamespace: cluster.ClusterName,
		machinesClient:   cluster.Management.Management.Machines(cluster.ClusterName),
	}

	m := &MachinesSyncer{
		clusterNamespace: cluster.ClusterName,
		machinesClient:   cluster.Management.Management.Machines(cluster.ClusterName),
		machines:         cluster.Management.Management.Machines(cluster.ClusterName).Controller().Lister(),
		clusters:         cluster.Management.Management.Clusters("").Controller().Lister(),
		nodes:            cluster.Core.Nodes("").Controller().Lister(),
	}
	cluster.Core.Nodes("").Controller().AddHandler("nodesSyncer", n.sync)
	cluster.Management.Management.Machines(cluster.ClusterName).Controller().AddHandler("machinesSyncer", m.sync)
}

func (n *NodeSyncer) sync(key string, node *corev1.Node) error {
	n.machinesClient.Controller().Enqueue(n.clusterNamespace, allMachineKey)
	return nil
}

func (m *MachinesSyncer) sync(key string, machine *v3.Machine) error {
	if key == fmt.Sprintf("%s/%s", m.clusterNamespace, allMachineKey) {
		return m.reconcileAll()
	}
	return nil
}

func (m *MachinesSyncer) reconcileAll() error {
	nodes, err := m.nodes.List("", labels.NewSelector())
	if err != nil {
		return err
	}

	nodeMap := make(map[string]*corev1.Node)
	for _, node := range nodes {
		nodeMap[node.Name] = node
	}

	machines, err := m.machines.List(m.clusterNamespace, labels.NewSelector())
	machineMap := make(map[string]*v3.Machine)
	for _, machine := range machines {
		nodeName := getNodeNameFromMachine(machine)
		if nodeName == "" {
			logrus.Warnf("Failed to get nodeName from machine [%s]", machine.Name)
			continue
		}
		machineMap[nodeName] = machine
	}

	// reconcile machines for existing nodes
	for name, node := range nodeMap {
		machine, _ := machineMap[name]
		remove := false
		if node.DeletionTimestamp != nil {
			if machine == nil {
				// machine is already removed
				continue
			}
			remove = true
		}
		err = m.reconcileMachineForNode(machine, remove, node)
		if err != nil {
			return err
		}
	}
	// run the logic for machine to remove
	for name, machine := range machineMap {
		if _, ok := nodeMap[name]; !ok {
			m.reconcileMachineForNode(machine, true, nil)
		}
	}

	return nil
}

func (m *MachinesSyncer) reconcileMachineForNode(machine *v3.Machine, remove bool, node *corev1.Node) error {
	if remove {
		return m.removeMachine(machine)
	}
	if machine == nil {
		return m.createMachine(node)
	} else {
		return m.updateMachine(machine, node)
	}
}

func (m *MachinesSyncer) removeMachine(machine *v3.Machine) error {
	err := m.machinesClient.Delete(machine.ObjectMeta.Name, nil)
	if err != nil {
		return errors.Wrapf(err, "Failed to delete machine [%s]", machine.Name)
	}
	logrus.Infof("Deleted cluster node [%s]", machine.Name)
	return nil
}

func (m *MachinesSyncer) updateMachine(existing *v3.Machine, node *corev1.Node) error {
	toUpdate, err := m.convertNodeToMachine(node, existing)
	if err != nil {
		return err
	}
	// update only when nothing changed
	if objectsAreEqual(existing, toUpdate) {
		return nil
	}
	logrus.Debugf("Updating machine for node [%s]", node.Name)
	_, err = m.machinesClient.Update(toUpdate)
	if err != nil {
		return errors.Wrapf(err, "Failed to update machine for node [%s]", node.Name)
	}
	logrus.Debugf("Updated machine for node [%s]", node.Name)
	return nil
}

func (m *MachinesSyncer) createMachine(node *corev1.Node) error {
	// try to get machine from api, in case cache didn't get the update
	existing, err := m.getMachine(node.Name, false)
	if err != nil {
		return err
	}
	if existing != nil {
		return nil
	}
	machine, err := m.convertNodeToMachine(node, existing)
	if err != nil {
		return err
	}

	_, err = m.machinesClient.Create(machine)
	if err != nil {
		return errors.Wrapf(err, "Failed to create machine for node [%s]", node.Name)
	}
	logrus.Infof("Created machine for node [%s]", node.Name)
	return nil
}

func (m *MachinesSyncer) getMachine(nodeName string, cache bool) (*v3.Machine, error) {
	var machines []*v3.Machine
	var err error
	if cache {
		machines, err = m.machines.List(m.clusterNamespace, labels.NewSelector())
		if err != nil {
			return nil, err
		}
	} else {
		machinelist, err := m.machinesClient.List(metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		for _, machine := range machinelist.Items {
			machines = append(machines, &machine)
		}
	}

	for _, machine := range machines {
		if machine.Status.NodeName == nodeName {
			return machine, nil
		}
		// to handle the case when machine was provisioned first
		if machine.Status.NodeConfig != nil {
			if machine.Status.NodeConfig.HostnameOverride == nodeName {
				return machine, nil
			}
		}
	}

	return nil, nil
}

func getNodeNameFromMachine(machine *v3.Machine) string {
	if machine.Status.NodeName != "" {
		return machine.Status.NodeName
	}
	// to handle the case when machine was provisioned first
	if machine.Status.NodeConfig != nil {
		if machine.Status.NodeConfig.HostnameOverride != "" {
			return machine.Status.NodeConfig.HostnameOverride
		}
	}
	return ""
}

func resetConditions(machine *v3.Machine) *v3.Machine {
	if machine.Status.NodeStatus.Conditions == nil {
		return machine
	}
	updated := machine.DeepCopy()
	var toUpdateConds []corev1.NodeCondition
	for _, cond := range machine.Status.NodeStatus.Conditions {
		toUpdateCond := cond.DeepCopy()
		toUpdateCond.LastHeartbeatTime = metav1.Time{}
		toUpdateCond.LastTransitionTime = metav1.Time{}
		toUpdateConds = append(toUpdateConds, *toUpdateCond)
	}
	updated.Status.NodeStatus.Conditions = toUpdateConds
	return updated
}

func objectsAreEqual(existing *v3.Machine, toUpdate *v3.Machine) bool {
	// we are updating spec and status only, so compare them
	toUpdateToCompare := resetConditions(toUpdate)
	existingToCompare := resetConditions(existing)
	statusEqual := reflect.DeepEqual(toUpdateToCompare.Status.NodeStatus, existingToCompare.Status.NodeStatus)
	labelsEqual := reflect.DeepEqual(toUpdateToCompare.Status.NodeLabels, existing.Status.NodeLabels)
	annotationsEqual := reflect.DeepEqual(toUpdateToCompare.Status.NodeAnnotations, existing.Status.NodeAnnotations)
	specEqual := reflect.DeepEqual(toUpdateToCompare.Spec.NodeSpec, existingToCompare.Spec.NodeSpec)
	nodeNameEqual := toUpdateToCompare.Status.NodeName == existingToCompare.Status.NodeName
	return statusEqual && specEqual && nodeNameEqual && labelsEqual && annotationsEqual
}

func (m *MachinesSyncer) convertNodeToMachine(node *corev1.Node, existing *v3.Machine) (*v3.Machine, error) {
	var machine *v3.Machine
	if existing == nil {
		machine = &v3.Machine{
			Spec:   v3.MachineSpec{},
			Status: v3.MachineStatus{},
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "machine-"},
		}
		machine.Namespace = m.clusterNamespace
		machine.Status.Requested = make(map[corev1.ResourceName]resource.Quantity)
		machine.Status.Limits = make(map[corev1.ResourceName]resource.Quantity)
		machine.Spec.NodeSpec = *node.Spec.DeepCopy()
		machine.Status.NodeStatus = *node.Status.DeepCopy()
	} else {
		machine = existing.DeepCopy()
		machine.Spec.NodeSpec = *node.Spec.DeepCopy()
		machine.Status.NodeStatus = *node.Status.DeepCopy()
		machine.Status.Requested = existing.Status.Requested
		machine.Status.Limits = existing.Status.Limits
	}

	machine.Status.NodeAnnotations = node.Annotations
	machine.Status.NodeLabels = node.Labels
	machine.Status.NodeName = node.Name
	machine.APIVersion = "management.cattle.io/v3"
	machine.Kind = "Machine"
	return machine, nil
}
