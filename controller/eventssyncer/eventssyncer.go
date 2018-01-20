package eventssyncer

import (
	"strings"

	"github.com/pkg/errors"
	"github.com/rancher/types/apis/core/v1"
	"github.com/rancher/types/apis/management.cattle.io/v3"
	"github.com/rancher/types/config"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	projectIDLabel = "field.cattle.io/projectId"
)

type EventsSyncer struct {
	clusterName               string
	clusterLister             v3.ClusterLister
	eventLister               v1.EventLister
	managementClient          kubernetes.Interface
	managementNamespaceLister v1.NamespaceLister
	clusterNamespaceLister    v1.NamespaceLister
}

func Register(cluster *config.ClusterContext) {
	e := &EventsSyncer{
		clusterName:               cluster.ClusterName,
		clusterLister:             cluster.Management.Management.Clusters("").Controller().Lister(),
		managementClient:          cluster.Management.K8sClient,
		managementNamespaceLister: cluster.Management.Core.Namespaces("").Controller().Lister(),
		eventLister:               cluster.Management.Core.Events("").Controller().Lister(),
		clusterNamespaceLister:    cluster.Core.Namespaces("").Controller().Lister(),
	}
	cluster.Core.Events("").Controller().AddHandler("events-syncer", e.sync)
}

func (e *EventsSyncer) sync(key string, event *corev1.Event) error {
	if event == nil {
		return nil
	}
	return e.createEvent(key, event)
}

func (e *EventsSyncer) createEvent(key string, event *corev1.Event) error {
	ns, err := e.getEventNamespaceName(event)
	if err != nil {
		if apierrors.IsNotFound(err) {
			logrus.Warnf("Error propagating event [%s]: %v", event.Message, err)
			return nil
		}
		return err
	}
	if ns == nil || ns.DeletionTimestamp != nil {
		return nil
	}
	existing, err := e.eventLister.Get(ns.Name, event.Name)
	if err == nil || apierrors.IsNotFound(err) {
		if existing != nil && existing.Name != "" {
			return nil
		}
		cluster, err := e.clusterLister.Get("", e.clusterName)
		if err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return errors.Wrapf(err, "Failed to get cluster [%s]", e.clusterName)
		}
		if cluster.DeletionTimestamp != nil {
			return nil
		}

		if event.InvolvedObject.Namespace == "" {
			logrus.Debugf("Skipping event [%s] as its involved object namespace is empty", event.Message)
			return nil
		}

		logrus.Debugf("Creating cluster event [%s]", event.Message)
		event := e.convertClusterEventToManagementEvent(event, ns)
		_, err = e.managementClient.Core().Events(event.Namespace).Create(event)
		return err
	}

	return err
}

func (e *EventsSyncer) convertClusterEventToManagementEvent(clusterEvent *corev1.Event, ns *corev1.Namespace) *corev1.Event {
	event := clusterEvent.DeepCopy()
	event.ObjectMeta = metav1.ObjectMeta{
		Name:        clusterEvent.Name,
		Labels:      clusterEvent.Labels,
		Annotations: clusterEvent.Annotations,
		Namespace:   ns.Name,
	}

	// event.namespace == event.InvolvedObject.Namespace
	event.InvolvedObject.Namespace = event.Namespace
	return event
}

func (e *EventsSyncer) getEventNamespaceName(event *corev1.Event) (*corev1.Namespace, error) {
	involedObjectNamespace := event.InvolvedObject.Namespace
	if involedObjectNamespace == "" {
		// cluster namespace, equals to cluster.name
		namespace, err := e.managementNamespaceLister.Get("", e.clusterName)
		if err != nil {
			return nil, err
		}
		return namespace, nil
	}

	// user namespace, derive from the project id
	// field.cattle.io/projectId value is <cluster name>:<project name>
	userNamespace, err := e.clusterNamespaceLister.Get("", involedObjectNamespace)
	if err != nil {
		return nil, errors.Wrapf(err, "Failed to find user namespace [%s]", e.clusterName)
	}
	if userNamespace.Annotations[projectIDLabel] != "" {
		parts := strings.Split(userNamespace.Annotations[projectIDLabel], ":")
		if len(parts) == 2 {
			// project namespace name == project name
			projectNamespaceName := parts[1]
			namespace, err := e.managementNamespaceLister.Get("", projectNamespaceName)
			if err != nil {
				return nil, err
			}
			return namespace, nil
		}
	}
	return nil, nil
}
