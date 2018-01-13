package authz

import (
	"github.com/pkg/errors"
	"github.com/rancher/types/apis/management.cattle.io/v3"
	"github.com/sirupsen/logrus"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

func (m *manager) syncCRTB(key string, binding *v3.ClusterRoleTemplateBinding) error {
	if binding == nil {
		return nil
	}

	if binding.DeletionTimestamp != nil {
		return m.ensureCRTBDelete(key, binding)
	}

	return m.ensureCRTB(key, binding)
}

func (m *manager) ensureCRTB(key string, binding *v3.ClusterRoleTemplateBinding) error {
	binding = binding.DeepCopy()
	if m.addFinalizer(binding) {
		if _, err := m.workload.Management.Management.ClusterRoleTemplateBindings(binding.Namespace).Update(binding); err != nil {
			return errors.Wrapf(err, "couldn't set finalizer on %v", key)
		}
	}

	if binding.RoleTemplateName == "" {
		logrus.Warnf("ClusterRoleTemplateBinding %v has no role template set. Skipping.", binding.Name)
		return nil
	}
	if binding.Subject.Name == "" {
		logrus.Warnf("Binding %v has no subject. Skipping", binding.Name)
		return nil
	}

	rt, err := m.rtLister.Get("", binding.RoleTemplateName)
	if err != nil {
		return errors.Wrapf(err, "couldn't get role template %v", binding.RoleTemplateName)
	}

	roles := map[string]*v3.RoleTemplate{}
	if err := m.gatherRoles(rt, roles); err != nil {
		return err
	}

	if err := m.ensureRoles(roles); err != nil {
		return errors.Wrap(err, "couldn't ensure roles")
	}

	for _, role := range roles {
		if err := m.ensureClusterBinding(role.Name, binding); err != nil {
			return errors.Wrapf(err, "couldn't ensure cluster binding %v %v", role.Name, binding.Subject.Name)
		}
	}

	return nil
}

func (m *manager) ensureClusterBinding(roleName string, binding *v3.ClusterRoleTemplateBinding) error {
	bindingCli := m.workload.K8sClient.RbacV1().ClusterRoleBindings()
	bindingName, objectMeta, subjects, roleRef := bindingParts(roleName, string(binding.UID), binding.Subject)
	if c, _ := m.crbLister.Get("", bindingName); c != nil {
		return nil
	}

	_, err := bindingCli.Create(&rbacv1.ClusterRoleBinding{
		ObjectMeta: objectMeta,
		Subjects:   subjects,
		RoleRef:    roleRef,
	})

	return err
}

func (m *manager) ensureCRTBDelete(key string, binding *v3.ClusterRoleTemplateBinding) error {
	if len(binding.ObjectMeta.Finalizers) <= 0 || binding.ObjectMeta.Finalizers[0] != m.finalizerName {
		return nil
	}

	binding = binding.DeepCopy()

	set := labels.Set(map[string]string{rtbOwnerLabel: string(binding.UID)})
	bindingCli := m.workload.K8sClient.RbacV1().ClusterRoleBindings()
	rbs, err := m.crbLister.List("", set.AsSelector())
	if err != nil {
		return errors.Wrapf(err, "couldn't list clusterrolebindings with selector %s", set.AsSelector())
	}

	for _, rb := range rbs {
		if err := bindingCli.Delete(rb.Name, &metav1.DeleteOptions{}); err != nil {
			if !apierrors.IsNotFound(err) {
				return errors.Wrapf(err, "error deleting clusterrolebinding %v", rb.Name)
			}
		}
	}

	if m.removeFinalizer(binding) {
		_, err := m.workload.Management.Management.ClusterRoleTemplateBindings(binding.Namespace).Update(binding)
		return err
	}
	return nil
}
