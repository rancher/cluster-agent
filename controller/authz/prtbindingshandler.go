package authz

import (
	"fmt"
	"strings"

	"github.com/pkg/errors"
	"github.com/rancher/norman/types/slice"
	typescorev1 "github.com/rancher/types/apis/core/v1"
	typesextv1beta1 "github.com/rancher/types/apis/extensions/v1beta1"
	"github.com/rancher/types/apis/management.cattle.io/v3"
	typesrbacv1 "github.com/rancher/types/apis/rbac.authorization.k8s.io/v1"
	"github.com/rancher/types/config"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

const (
	finalizerName  = "prtbFinalizer"
	prtbOwnerLabel = "io.cattle.prtb.owner"
	projectIDLabel = "io.cattle.field.projectId"
)

func Register(workload *config.ClusterContext) {
	r := &roleHandler{
		workload:   workload,
		rtLister:   workload.Management.Management.RoleTemplates("").Controller().Lister(),
		psptLister: workload.Management.Management.PodSecurityPolicyTemplates("").Controller().Lister(),
		nsLister:   workload.Core.Namespaces("").Controller().Lister(),
		rbLister:   workload.RBAC.RoleBindings("").Controller().Lister(),
		crbLister:  workload.RBAC.ClusterRoleBindings("").Controller().Lister(),
		crLister:   workload.RBAC.ClusterRoles("").Controller().Lister(),
		pspLister:  workload.Extensions.PodSecurityPolicies("").Controller().Lister(),
	}
	workload.Management.Management.ProjectRoleTemplateBindings("").Controller().AddHandler(r.syncPRTB)
}

type roleHandler struct {
	workload   *config.ClusterContext
	rtLister   v3.RoleTemplateLister
	psptLister v3.PodSecurityPolicyTemplateLister
	nsLister   typescorev1.NamespaceLister
	crLister   typesrbacv1.ClusterRoleLister
	crbLister  typesrbacv1.ClusterRoleBindingLister
	rbLister   typesrbacv1.RoleBindingLister
	pspLister  typesextv1beta1.PodSecurityPolicyLister
}

func (r *roleHandler) syncPRTB(key string, binding *v3.ProjectRoleTemplateBinding) error {
	if binding == nil {
		return nil
	}

	if binding.DeletionTimestamp != nil {
		return r.ensurePRTBDelete(key, binding)
	}

	return r.ensurePRTB(key, binding)
}

func (r *roleHandler) ensurePRTB(key string, binding *v3.ProjectRoleTemplateBinding) error {
	if err := r.addFinalizer(binding); err != nil {
		return errors.Wrapf(err, "couldn't ensure finalizer set on %v", key)
	}

	rt, err := r.rtLister.Get("", binding.RoleTemplateName)
	if err != nil {
		return errors.Wrapf(err, "couldn't get role template %v", binding.RoleTemplateName)
	}

	// Get namespaces belonging to project
	set := labels.Set(map[string]string{projectIDLabel: binding.ProjectName})
	namespaces, err := r.nsLister.List("", set.AsSelector())
	if err != nil {
		return errors.Wrapf(err, "couldn't list namespaces with selector %s", set.AsSelector())
	}
	if len(namespaces) == 0 {
		return nil
	}

	roles := map[string]*v3.RoleTemplate{}
	if err := r.gatherRoles(rt, roles); err != nil {
		return err
	}

	if err := r.ensureRoles(roles); err != nil {
		return errors.Wrap(err, "couldn't ensure roles")
	}

	// TODO is .Items the complete list or is there potential pagination to deal with?
	for _, ns := range namespaces {
		for _, role := range roles {
			if err := r.ensureBinding(ns.Name, role.Name, binding); err != nil {
				return errors.Wrapf(err, "couldn't ensure binding %v %v in %v", role.Name, binding.Subject.Name, ns.Name)
			}
		}
	}

	return nil
}

func (r *roleHandler) ensurePRTBDelete(key string, binding *v3.ProjectRoleTemplateBinding) error {
	if len(binding.ObjectMeta.Finalizers) <= 0 || binding.ObjectMeta.Finalizers[0] != finalizerName {
		return nil
	}

	binding = binding.DeepCopy()

	// Get namespaces belonging to project
	set := labels.Set(map[string]string{projectIDLabel: binding.ProjectName})
	namespaces, err := r.nsLister.List("", set.AsSelector())
	if err != nil {
		return errors.Wrapf(err, "couldn't list namespaces with selector %s", set.AsSelector())
	}
	if len(namespaces) == 0 {
		return nil
	}

	set = labels.Set(map[string]string{prtbOwnerLabel: string(binding.UID)})
	for _, ns := range namespaces {
		bindingCli := r.workload.K8sClient.RbacV1().RoleBindings(ns.Name)
		rbs, err := r.rbLister.List(ns.Name, set.AsSelector())
		if err != nil {
			return errors.Wrapf(err, "couldn't list rolebindings with selector %s", set.AsSelector())
		}

		for _, rb := range rbs {
			if err := bindingCli.Delete(rb.Name, &metav1.DeleteOptions{}); err != nil {
				return errors.Wrapf(err, "error deleting rolebinding %v", rb.Name)
			}
		}
	}

	return r.removeFinalizer(binding)
}

func (r *roleHandler) gatherRoles(rt *v3.RoleTemplate, roleTemplates map[string]*v3.RoleTemplate) error {
	roleTemplates[rt.Name] = rt

	for _, rtName := range rt.RoleTemplateNames {
		subRT, err := r.rtLister.Get("", rtName)
		if err != nil {
			return errors.Wrapf(err, "couldn't get RoleTemplate %s", rtName)
		}
		if err := r.gatherRoles(subRT, roleTemplates); err != nil {
			return errors.Wrapf(err, "couldn't gather RoleTemplate %s", rtName)
		}
	}

	return nil
}

func (r *roleHandler) addFinalizer(binding *v3.ProjectRoleTemplateBinding) error {
	if slice.ContainsString(binding.Finalizers, finalizerName) {
		return nil
	}

	binding = binding.DeepCopy()
	binding.Finalizers = append(binding.Finalizers, finalizerName)
	_, err := r.workload.Management.Management.ProjectRoleTemplateBindings("").Update(binding)
	return err
}

func (r *roleHandler) removeFinalizer(binding *v3.ProjectRoleTemplateBinding) error {
	if !slice.ContainsString(binding.Finalizers, finalizerName) {
		return nil
	}

	var finalizers []string
	for _, finalizer := range binding.Finalizers {
		if finalizer == finalizerName {
			continue
		}
		finalizers = append(finalizers, finalizer)
	}
	binding.ObjectMeta.Finalizers = finalizers
	_, err := r.workload.Management.Management.ProjectRoleTemplateBindings("").Update(binding)
	return err
}

func (r *roleHandler) ensureRoles(rts map[string]*v3.RoleTemplate) error {
	roleCli := r.workload.K8sClient.RbacV1().ClusterRoles()
	for _, rt := range rts {
		if rt.Builtin {
			// TODO assert the role exists and log an error if it doesnt.
			continue
		}

		if role, err := r.crLister.Get("", rt.Name); err == nil {
			role = role.DeepCopy()
			// TODO potentially check a version so that we don't do unnecessary updates
			role.Rules = rt.Rules
			_, err := roleCli.Update(role)
			if err != nil {
				return errors.Wrapf(err, "couldn't update role %v", rt.Name)
			}
		}

		_, err := roleCli.Create(&rbacv1.ClusterRole{
			ObjectMeta: metav1.ObjectMeta{
				Name: rt.Name,
			},
			Rules: rt.Rules,
		})
		if err != nil {
			return errors.Wrapf(err, "couldn't create role %v", rt.Name)
		}
	}

	return nil
}

func (r *roleHandler) ensureBinding(ns, roleName string, binding *v3.ProjectRoleTemplateBinding) error {
	bindingCli := r.workload.K8sClient.RbacV1().RoleBindings(ns)
	// TODO dont use name, use a UID
	bindingName := strings.ToLower(fmt.Sprintf("%v-%v-%v", roleName, binding.Subject.Name, binding.UID))
	_, err := r.rbLister.Get(ns, bindingName)
	if err == nil {
		return nil
	}

	_, err = bindingCli.Create(&rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:   bindingName,
			Labels: map[string]string{prtbOwnerLabel: string(binding.UID)},
		},
		Subjects: []rbacv1.Subject{binding.Subject},
		RoleRef: rbacv1.RoleRef{
			Kind: "ClusterRole",
			Name: roleName,
		},
	})

	return err
}
