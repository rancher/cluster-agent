package healthsyncer

import (
	"fmt"
	"time"

	clusterv1 "github.com/rancher/types/apis/cluster.cattle.io/v1"
	corev1 "github.com/rancher/types/apis/core/v1"
	"github.com/rancher/types/config"
	"github.com/sirupsen/logrus"
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	syncInterval = 15 * time.Second
)

type HealthSyncer struct {
	clusterName       string
	Clusters          clusterv1.ClusterInterface
	ComponentStatuses corev1.ComponentStatusInterface
}

func Register(workload *config.WorkloadContext) {
	h := &HealthSyncer{
		clusterName:       workload.ClusterName,
		Clusters:          workload.Cluster.Cluster.Clusters(""),
		ComponentStatuses: workload.Core.ComponentStatuses(""),
	}

	go h.syncHealth(syncInterval)
}

func (h *HealthSyncer) syncHealth(syncHealth time.Duration) {
	for range time.Tick(syncHealth) {
		err := h.updateClusterHealth()
		if err != nil {
			logrus.Info(err)
		}
	}
}

func (h *HealthSyncer) updateClusterHealth() error {
	cluster, err := h.getCluster()
	if err != nil {
		return err
	}
	if cluster == nil {
		logrus.Info("Skip updating cluster health, cluster [%s] deleted", h.clusterName)
		return nil
	}
	if !isProvisioned(cluster) {
		return fmt.Errorf("Skip updating cluster health - cluster [%s] not provisioned yet", h.clusterName)
	}
	cses, err := h.ComponentStatuses.List(metav1.ListOptions{})
	if err != nil {
		logrus.Debugf("Error getting componentstatuses for server health %v", err)
		updateConditionStatus(cluster, clusterv1.ClusterConditionReady, v1.ConditionFalse)
		return nil
	}
	updateConditionStatus(cluster, clusterv1.ClusterConditionReady, v1.ConditionTrue)
	logrus.Infof("Cluster [%s] Condition Ready", h.clusterName)

	h.updateClusterStatus(cluster, cses.Items)
	_, err = h.Clusters.Update(cluster)
	if err != nil {
		return fmt.Errorf("Failed to update cluster [%s] %v", cluster.Name, err)
	}
	logrus.Infof("Updated cluster health successfully [%s]", h.clusterName)
	return nil
}

func (h *HealthSyncer) updateClusterStatus(cluster *clusterv1.Cluster, cses []v1.ComponentStatus) {
	for _, cs := range cses {
		clusterCS := convertToClusterComponentStatus(&cs)
		cluster.Status.ComponentStatuses = []clusterv1.ClusterComponentStatus{*clusterCS}
	}
}

func (h *HealthSyncer) getCluster() (*clusterv1.Cluster, error) {
	return h.Clusters.Get(h.clusterName, metav1.GetOptions{})
}

func convertToClusterComponentStatus(cs *v1.ComponentStatus) *clusterv1.ClusterComponentStatus {
	return &clusterv1.ClusterComponentStatus{
		Name:       cs.Name,
		Conditions: cs.Conditions,
	}
}

func isProvisioned(cluster *clusterv1.Cluster) bool {
	isProvisioned := getConditionByType(cluster, clusterv1.ClusterConditionProvisioned)
	if isProvisioned == nil {
		return false
	}
	return isProvisioned.Status == "True"
}

func updateConditionStatus(cluster *clusterv1.Cluster, conditionType clusterv1.ClusterConditionType, status v1.ConditionStatus) {
	condition := getConditionByType(cluster, conditionType)
	currTime := time.Now().UTC().String()
	if condition != nil {
		condition.Status = status
		condition.LastUpdateTime = currTime
	} else {
		ncondition := &clusterv1.ClusterCondition{
			Status:         status,
			LastUpdateTime: currTime,
		}
		cluster.Status.Conditions = append(cluster.Status.Conditions, *ncondition)
	}
}

func getConditionByType(cluster *clusterv1.Cluster, conditionType clusterv1.ClusterConditionType) *clusterv1.ClusterCondition {
	for _, condition := range cluster.Status.Conditions {
		if condition.Type == conditionType {
			return &condition
		}
	}
	return nil
}
