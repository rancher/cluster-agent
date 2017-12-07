package e2e

import (
	"testing"

	"github.com/rancher/cluster-agent/controller/authz"
	authzv1 "github.com/rancher/types/apis/management.cattle.io/v3"
	"github.com/rancher/types/config"
	"gopkg.in/check.v1"
	extv1beta1 "k8s.io/api/extensions/v1beta1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	extclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	clientset "k8s.io/client-go/kubernetes"
)

// Hook up gocheck into the "go test" runner.
func Test(t *testing.T) { check.TestingT(t) }

type AuthzSuite struct {
	extClient     *extclient.Clientset
	clusterClient *clientset.Clientset
	ctx           *config.ClusterContext
}

var _ = check.Suite(&AuthzSuite{})

func (s *AuthzSuite) TestRoleTemplateBindingCreate(c *check.C) {
	// create project
	projectName := "test-project-1"

	// create a PodSecurityPolicyTemplate to be referenced in a PolicyRule
	pspName := "podsecuritypolicy-1"
	s.clusterClient.ExtensionsV1beta1().PodSecurityPolicies().Delete(pspName, &metav1.DeleteOptions{})
	pspTemplate, err := s.ctx.Management.Management.PodSecurityPolicyTemplates("").Create(&authzv1.PodSecurityPolicyTemplate{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PodSecurityPolicyTemplates",
			APIVersion: "management.cattle.io/v3",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: pspName,
		},
		Spec: extv1beta1.PodSecurityPolicySpec{
			AllowedHostPaths:       []extv1beta1.AllowedHostPath{{"/tmp"}},
			ReadOnlyRootFilesystem: true,
			SELinux:                extv1beta1.SELinuxStrategyOptions{Rule: extv1beta1.SELinuxStrategyRunAsAny},
			RunAsUser:              extv1beta1.RunAsUserStrategyOptions{Rule: extv1beta1.RunAsUserStrategyMustRunAsNonRoot},
			SupplementalGroups:     extv1beta1.SupplementalGroupsStrategyOptions{Rule: extv1beta1.SupplementalGroupsStrategyRunAsAny},
			FSGroup:                extv1beta1.FSGroupStrategyOptions{Rule: extv1beta1.FSGroupStrategyRunAsAny},
		},
	})
	c.Assert(err, check.IsNil)
	c.Assert(pspTemplate.Name, check.Equals, pspName)
	c.Assert(pspTemplate.Spec.ReadOnlyRootFilesystem, check.Equals, true)
	c.Assert(pspTemplate.Spec.AllowedHostPaths, check.DeepEquals, []extv1beta1.AllowedHostPath{{"/tmp"}})

	// create ProjectRoleTemplate (this one will be referenced by the next one)
	podRORoleTemplateName := "pod-readonly"
	rt, err := s.createProjectRoleTemplate(podRORoleTemplateName,
		[]rbacv1.PolicyRule{
			{[]string{"get", "list", "watch"}, []string{""}, []string{"pods"}, []string{}, []string{}},
			{[]string{"use"}, []string{"extensions"}, []string{"podsecuritypolicies"}, []string{pspName}, []string{}},
		}, []string{}, false, c)

	// create ProjectRoleTemplate that user will be bound to
	rtName := "readonly"
	rt2, err := s.createProjectRoleTemplate(rtName,
		[]rbacv1.PolicyRule{
			{[]string{"get", "list", "watch"}, []string{"apps", "extensions"}, []string{"deployments"}, []string{}, []string{}},
		},
		[]string{podRORoleTemplateName}, false, c)

	// create namespace and watchers for resources in that namespace
	ns := setupNS("authz-test-ns1", projectName, s.clusterClient.CoreV1().Namespaces(), c)
	roleWatcher := s.roleWatcher(ns.Name, c)
	bindingWatcher := s.bindingWatcher(ns.Name, c)
	pspWatcher := s.pspWatcher(c)
	defer roleWatcher.Stop()
	defer bindingWatcher.Stop()
	defer pspWatcher.Stop()

	// create ProjectRoleTemplateBinding
	subject := rbacv1.Subject{
		Kind: "User",
		Name: "user1",
	}
	s.createPRTBinding("readonly-binding-1", subject, projectName, rtName, c)

	// assert corresponding role is created with all the rules
	watchChecker(roleWatcher, c, func(watchEvent watch.Event) bool {
		if watch.Modified == watchEvent.Type || watch.Added == watchEvent.Type {
			if role, ok := watchEvent.Object.(*rbacv1.Role); ok {
				allRules := []rbacv1.PolicyRule{}
				allRules = append(allRules, rt2.Rules...)
				allRules = append(allRules, rt.Rules...)
				c.Assert(role.Rules, check.DeepEquals, allRules)
				c.Assert(role.Name, check.Equals, rtName)
				return true
			}
		}
		return false
	})

	// assert binding is created properly
	watchChecker(bindingWatcher, c, func(watchEvent watch.Event) bool {
		if watch.Modified == watchEvent.Type || watch.Added == watchEvent.Type {
			if binding, ok := watchEvent.Object.(*rbacv1.RoleBinding); ok {
				c.Assert(binding.Subjects[0].Kind, check.Equals, subject.Kind)
				c.Assert(binding.Subjects[0].Name, check.Equals, subject.Name)
				c.Assert(binding.RoleRef.Name, check.Equals, rtName)
				return true
			}
		}
		return false
	})

	// asser psp is created properly
	watchChecker(pspWatcher, c, func(watchEvent watch.Event) bool {
		if watch.Modified == watchEvent.Type || watch.Added == watchEvent.Type {
			if psp, ok := watchEvent.Object.(*extv1beta1.PodSecurityPolicy); ok {
				c.Assert(psp.Spec, check.DeepEquals, pspTemplate.Spec)
				return true
			}
		}
		return false
	})
}

func (s *AuthzSuite) TestBuiltinRoleTemplateBindingCreate(c *check.C) {
	// create project
	projectName := "test-project-2"

	// create ProjectRoleTemplate that user will be bound to
	rtName := "view"
	_, err := s.createProjectRoleTemplate(rtName,
		[]rbacv1.PolicyRule{}, []string{}, true, c)

	// create namespace and watchers for resources in that namespace
	ns := setupNS("authz-builtin-test-ns1", projectName, s.clusterClient.CoreV1().Namespaces(), c)
	bindingWatcher := s.bindingWatcher("authz-builtin-test-ns1", c)
	defer bindingWatcher.Stop()

	roles, err := s.clusterClient.RbacV1().Roles(ns.Name).List(metav1.ListOptions{})
	c.Assert(err, check.IsNil)
	rolesPreCount := len(roles.Items)

	// create ProjectRoleTemplateBinding
	subject := rbacv1.Subject{
		Kind: "User",
		Name: "user1",
	}
	s.createPRTBinding("builtin-binding-1", subject, projectName, rtName, c)

	// assert binding is created properly
	watchChecker(bindingWatcher, c, func(watchEvent watch.Event) bool {
		if watch.Modified == watchEvent.Type || watch.Added == watchEvent.Type {
			if binding, ok := watchEvent.Object.(*rbacv1.RoleBinding); ok {
				c.Assert(binding.Subjects[0].Kind, check.Equals, subject.Kind)
				c.Assert(binding.Subjects[0].Name, check.Equals, subject.Name)
				c.Assert(binding.RoleRef.Name, check.Equals, rtName)
				return true
			}
		}
		return false
	})

	// ensure no new roles were created in the namespace
	roles, err = s.clusterClient.RbacV1().Roles(ns.Name).List(metav1.ListOptions{})
	c.Assert(err, check.IsNil)
	rolesPostCount := len(roles.Items)
	c.Assert(rolesPostCount, check.Equals, rolesPreCount)
}

func (s *AuthzSuite) createPRTBinding(bindingName string, subject rbacv1.Subject, projectName string, rtName string, c *check.C) *authzv1.ProjectRoleTemplateBinding {
	binding, err := s.ctx.Management.Management.ProjectRoleTemplateBindings("").Create(&authzv1.ProjectRoleTemplateBinding{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ProjectRoleTemplateBinding",
			APIVersion: "management.cattle.io/v3",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: bindingName,
		},
		Subject:                 subject,
		ProjectName:             projectName,
		ProjectRoleTemplateName: rtName,
	})

	c.Assert(err, check.IsNil)
	c.Assert(binding.Name, check.Equals, bindingName)
	return binding
}

func (s *AuthzSuite) createProjectRoleTemplate(name string, rules []rbacv1.PolicyRule, prts []string, builtin bool, c *check.C) (*authzv1.ProjectRoleTemplate, error) {
	rt, err := s.ctx.Management.Management.ProjectRoleTemplates("").Create(&authzv1.ProjectRoleTemplate{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ProjectRoleTemplate",
			APIVersion: "management.cattle.io/v3",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Rules: rules,
		ProjectRoleTemplateNames: prts,
		Builtin:                  builtin,
	})
	c.Assert(err, check.IsNil)
	c.Assert(rt.Name, check.Equals, name)
	return rt, err
}

func (s *AuthzSuite) pspWatcher(c *check.C) watch.Interface {
	pspClient := s.clusterClient.ExtensionsV1beta1().PodSecurityPolicies()
	pList, err := pspClient.List(metav1.ListOptions{})
	c.Assert(err, check.IsNil)
	pListMeta, err := meta.ListAccessor(pList)
	c.Assert(err, check.IsNil)
	pspWatch, err := pspClient.Watch(metav1.ListOptions{ResourceVersion: pListMeta.GetResourceVersion()})
	c.Assert(err, check.IsNil)
	return pspWatch
}

func (s *AuthzSuite) bindingWatcher(namespace string, c *check.C) watch.Interface {
	bindingClient := s.clusterClient.RbacV1().RoleBindings(namespace)
	bList, err := bindingClient.List(metav1.ListOptions{})
	c.Assert(err, check.IsNil)
	bListMeta, err := meta.ListAccessor(bList)
	c.Assert(err, check.IsNil)
	bindingWatch, err := bindingClient.Watch(metav1.ListOptions{ResourceVersion: bListMeta.GetResourceVersion()})
	c.Assert(err, check.IsNil)
	return bindingWatch
}

func (s *AuthzSuite) roleWatcher(namespace string, c *check.C) watch.Interface {
	roleClient := s.clusterClient.RbacV1().Roles(namespace)
	initialList, err := roleClient.List(metav1.ListOptions{})
	c.Assert(err, check.IsNil)
	initialListListMeta, err := meta.ListAccessor(initialList)
	c.Assert(err, check.IsNil)
	roleWatch, err := roleClient.Watch(metav1.ListOptions{ResourceVersion: initialListListMeta.GetResourceVersion()})
	c.Assert(err, check.IsNil)
	return roleWatch
}

func (s *AuthzSuite) SetUpSuite(c *check.C) {
	clusterClient, extClient, workload := clientForSetup(c)
	s.extClient = extClient
	s.clusterClient = clusterClient
	s.ctx = workload
	s.setupCRDs(c)

	authz.Register(workload)

	go func() {
		err := workload.StartAndWait()
		c.Assert(err, check.IsNil)
	}()
}

func (s *AuthzSuite) setupCRDs(c *check.C) {
	crdClient := s.extClient.ApiextensionsV1beta1().CustomResourceDefinitions()

	initialList, err := crdClient.List(metav1.ListOptions{})
	c.Assert(err, check.IsNil)

	initialListListMeta, err := meta.ListAccessor(initialList)
	c.Assert(err, check.IsNil)

	crdWatch, err := crdClient.Watch(metav1.ListOptions{ResourceVersion: initialListListMeta.GetResourceVersion()})
	c.Assert(err, check.IsNil)
	defer crdWatch.Stop()

	setupCRD("projectroletemplate", "projectroletemplates", "management.cattle.io", "ProjectRoleTemplate", "v3",
		apiextensionsv1beta1.ClusterScoped, crdClient, crdWatch, c)

	setupCRD("projectroletemplatebinding", "projectroletemplatebindings", "management.cattle.io", "ProjectRoleTemplateBinding", "v3",
		apiextensionsv1beta1.ClusterScoped, crdClient, crdWatch, c)

	setupCRD("podsecuritypolicytemplate", "podsecuritypolicytemplates", "management.cattle.io", "PodSecurityPolicyTemplates", "v3",
		apiextensionsv1beta1.ClusterScoped, crdClient, crdWatch, c)
}
