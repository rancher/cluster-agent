package utils

import (
	clusterv1 "github.com/rancher/types/apis/cluster.cattle.io/v1"
)

func IsClusterProvisioned(cluster *clusterv1.Cluster) bool {
	isProvisioned := getClusterConditionByType(cluster, clusterv1.ClusterConditionProvisioned)
	if isProvisioned == nil {
		return false
	}
	return isProvisioned.Status == "True"
}

func getClusterConditionByType(cluster *clusterv1.Cluster, conditionType clusterv1.ClusterConditionType) *clusterv1.ClusterCondition {
	for _, condition := range cluster.Status.Conditions {
		if condition.Type == conditionType {
			return &condition
		}
	}
	return nil
}
