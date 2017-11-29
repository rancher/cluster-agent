package e2e

import (
	"os"
	"testing"
	"time"

	"github.com/rancher/cluster-agent/controller/authz"
	authzv1 "github.com/rancher/types/apis/authorization.cattle.io/v1"
	"github.com/rancher/types/config"
	"gopkg.in/check.v1"
	corev1 "k8s.io/api/core/v1"
	extv1beta1 "k8s.io/api/extensions/v1beta1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	extclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	crdclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/typed/apiextensions/v1beta1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// Hook up gocheck into the "go test" runner.
func Test(t *testing.T) { check.TestingT(t) }

type AuthzSuite struct {
	extClient     *extclient.Clientset
	clusterClient *clientset.Clientset
	workloadCtx   *config.WorkloadContext
}

var _ = check.Suite(&AuthzSuite{})

func (s *AuthzSuite) TestRoleTemplateBindingCreate(c *check.C) {
	// create project
	projectName := "test-project-1"

	// create a PodSecurityPolicyTemplate to be referenced in a PolicyRule
	pspName := "podsecuritypolicy-1"
	s.clusterClient.ExtensionsV1beta1().PodSecurityPolicies().Delete(pspName, &metav1.DeleteOptions{})
	pspTemplate, err := s.workloadCtx.Cluster.Authorization.PodSecurityPolicyTemplates("").Create(&authzv1.PodSecurityPolicyTemplate{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PodSecurityPolicyTemplates",
			APIVersion: "authorization.cattle.io/v1",
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
		}, []string{}, c)

	// create ProjectRoleTemplate that user will be bound to
	rtName := "readonly"
	rt2, err := s.createProjectRoleTemplate(rtName,
		[]rbacv1.PolicyRule{
			{[]string{"get", "list", "watch"}, []string{"apps", "extensions"}, []string{"deployments"}, []string{}, []string{}},
		},
		[]string{podRORoleTemplateName}, c)

	// create namespace and watchers for resources in that namespace
	ns := s.setupNS("authz-test-ns1", projectName, c)
	roleWatcher, bindingWatcher, pspWatcher := s.watchers(ns.Name, c)
	defer roleWatcher.Stop()
	defer bindingWatcher.Stop()
	defer pspWatcher.Stop()

	// create ProjectRoleTemplateBinding
	bindingName := "readonly-binding-1"
	subject := rbacv1.Subject{
		Kind: "User",
		Name: "user1",
	}
	binding, err := s.workloadCtx.Cluster.Authorization.ProjectRoleTemplateBindings("").Create(&authzv1.ProjectRoleTemplateBinding{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ProjectRoleTemplateBinding",
			APIVersion: "authorization.cattle.io/v1",
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

	// assert corresponding role and rolebinding is created in proper NS
Loop:
	for {
		select {
		case watchEvent := <-roleWatcher.ResultChan():
			if watch.Modified == watchEvent.Type || watch.Added == watchEvent.Type {
				if role, ok := watchEvent.Object.(*rbacv1.Role); ok {

					allRules := []rbacv1.PolicyRule{}
					allRules = append(allRules, rt2.Rules...)
					allRules = append(allRules, rt.Rules...)

					c.Assert(role.Rules, check.DeepEquals, allRules)
					c.Assert(role.Name, check.Equals, rtName)
					break Loop
				}
			}
		case <-time.After(5 * time.Second):
			c.Fatalf("Timeout waiting for role to exist")
		}
	}

Loop2:
	for {
		select {
		case watchEvent := <-bindingWatcher.ResultChan():
			if watch.Modified == watchEvent.Type || watch.Added == watchEvent.Type {
				if binding, ok := watchEvent.Object.(*rbacv1.RoleBinding); ok {
					c.Assert(binding.Subjects[0].Kind, check.Equals, subject.Kind)
					c.Assert(binding.Subjects[0].Name, check.Equals, subject.Name)
					c.Assert(binding.RoleRef.Name, check.Equals, rtName)
					break Loop2
				}
			}
		case <-time.After(5 * time.Second):
			c.Fatalf("Timeout waiting for binding to exist")
		}
	}

Loop3:
	for {
		select {
		case watchEvent := <-pspWatcher.ResultChan():
			if watch.Modified == watchEvent.Type || watch.Added == watchEvent.Type {
				if psp, ok := watchEvent.Object.(*extv1beta1.PodSecurityPolicy); ok {
					c.Assert(psp.Spec, check.DeepEquals, pspTemplate.Spec)
					break Loop3
				}
			}
		case <-time.After(5 * time.Second):
			c.Fatalf("Timeout waiting for podSecurityPolicy to exist")
		}
	}
}

func (s *AuthzSuite) createProjectRoleTemplate(name string, rules []rbacv1.PolicyRule, prts []string, c *check.C) (*authzv1.ProjectRoleTemplate, error) {
	rt, err := s.workloadCtx.Cluster.Authorization.ProjectRoleTemplates("").Create(&authzv1.ProjectRoleTemplate{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ProjectRoleTemplate",
			APIVersion: "authorization.cattle.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Rules: rules,
		ProjectRoleTemplateNames: prts,
	})
	c.Assert(err, check.IsNil)
	c.Assert(rt.Name, check.Equals, name)
	return rt, err
}

func (s *AuthzSuite) watchers(namespace string, c *check.C) (watch.Interface, watch.Interface, watch.Interface) {
	// role watcher
	roleClient := s.clusterClient.RbacV1().Roles(namespace)
	initialList, err := roleClient.List(metav1.ListOptions{})
	c.Assert(err, check.IsNil)
	initialListListMeta, err := meta.ListAccessor(initialList)
	c.Assert(err, check.IsNil)
	roleWatch, err := roleClient.Watch(metav1.ListOptions{ResourceVersion: initialListListMeta.GetResourceVersion()})
	c.Assert(err, check.IsNil)

	// binding watcher
	bindingClient := s.clusterClient.RbacV1().RoleBindings(namespace)
	bList, err := bindingClient.List(metav1.ListOptions{})
	c.Assert(err, check.IsNil)
	bListMeta, err := meta.ListAccessor(bList)
	c.Assert(err, check.IsNil)
	bindingWatch, err := bindingClient.Watch(metav1.ListOptions{ResourceVersion: bListMeta.GetResourceVersion()})
	c.Assert(err, check.IsNil)

	// psp watcher
	pspClient := s.clusterClient.ExtensionsV1beta1().PodSecurityPolicies()
	pList, err := pspClient.List(metav1.ListOptions{})
	c.Assert(err, check.IsNil)
	pListMeta, err := meta.ListAccessor(pList)
	c.Assert(err, check.IsNil)
	pspWatch, err := pspClient.Watch(metav1.ListOptions{ResourceVersion: pListMeta.GetResourceVersion()})
	c.Assert(err, check.IsNil)

	return roleWatch, bindingWatch, pspWatch
}

func (s *AuthzSuite) setupNS(name, projectName string, c *check.C) *corev1.Namespace {
	nsClient := s.clusterClient.CoreV1().Namespaces()

	if _, err := nsClient.Get(name, metav1.GetOptions{}); err == nil {
		nsList, err := nsClient.List(metav1.ListOptions{})
		c.Assert(err, check.IsNil)
		nsListMeta, err := meta.ListAccessor(nsList)
		c.Assert(err, check.IsNil)
		nsWatch, err := nsClient.Watch(metav1.ListOptions{ResourceVersion: nsListMeta.GetResourceVersion()})
		c.Assert(err, check.IsNil)
		defer nsWatch.Stop()

		if err := s.clusterClient.CoreV1().Namespaces().Delete(name, &metav1.DeleteOptions{}); err != nil {
			c.Fatal(err)
		}

	Loop:
		for {
			select {
			case watchEvent := <-nsWatch.ResultChan():
				if watch.Deleted == watchEvent.Type || watch.Modified == watchEvent.Type {
					if ns, ok := watchEvent.Object.(*corev1.Namespace); ok && ns.Name == name {
						for i := 0; i < 10; i++ {
							if ns, err := nsClient.Get(name, metav1.GetOptions{}); err == nil {
								if ns.Status.Phase == corev1.NamespaceTerminating && len(ns.Spec.Finalizers) == 0 {
									break Loop
								}
							} else {
								break Loop
							}
							time.Sleep(time.Second)
						}
					}
				}
			case <-time.After(5 * time.Second):
				c.Fatalf("Timeout waiting for namesapce to delete")
			}
		}
	}

	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				"project": projectName,
			},
			Annotations: map[string]string{
				"project": projectName,
			},
		},
	}
	ns, err := s.clusterClient.CoreV1().Namespaces().Create(ns)
	c.Assert(err, check.IsNil)

	return ns
}

func (s *AuthzSuite) SetUpSuite(c *check.C) {
	clusterClient, extClient, workload := clientForSetup(c)
	s.extClient = extClient
	s.clusterClient = clusterClient
	s.workloadCtx = workload
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

	setupCRD("projectroletemplate", "projectroletemplates", "authorization.cattle.io", "ProjectRoleTemplate", "v1",
		apiextensionsv1beta1.ClusterScoped, crdClient, crdWatch, c)

	setupCRD("projectroletemplatebinding", "projectroletemplatebindings", "authorization.cattle.io", "ProjectRoleTemplateBinding", "v1",
		apiextensionsv1beta1.ClusterScoped, crdClient, crdWatch, c)

	setupCRD("podsecuritypolicytemplate", "podsecuritypolicytemplates", "authorization.cattle.io", "PodSecurityPolicyTemplates", "v1",
		apiextensionsv1beta1.ClusterScoped, crdClient, crdWatch, c)
}

func setupCRD(name, plural, group, kind, version string, scope apiextensionsv1beta1.ResourceScope, crdClient crdclient.CustomResourceDefinitionInterface,
	crdWatch watch.Interface, c *check.C) { // *apiextensionsv1beta1.CustomResourceDefinition {
	fullName := plural + "." + group

	if err := crdClient.Delete(fullName, &metav1.DeleteOptions{}); err == nil {
		waitForCRDDeleted(fullName, crdWatch, crdClient, c)
	}

	crd := newCRD(fullName, name, plural, group, kind, version, scope)
	_, err := crdClient.Create(crd)
	c.Assert(err, check.IsNil)
	waitForCRDEstablished(fullName, crdWatch, crdClient, c)
}

func waitForCRDEstablished(name string, crdWatch watch.Interface, crdClient crdclient.CustomResourceDefinitionInterface, c *check.C) {
Loop:
	for {
		select {
		case watchEvent := <-crdWatch.ResultChan():
			if watch.Modified == watchEvent.Type || watch.Added == watchEvent.Type {
				if crd, ok := watchEvent.Object.(*apiextensionsv1beta1.CustomResourceDefinition); ok && crd.Name == name {
					got, err := crdClient.Get(name, metav1.GetOptions{})
					c.Assert(err, check.IsNil)

					for _, c := range got.Status.Conditions {
						if apiextensionsv1beta1.Established == c.Type && apiextensionsv1beta1.ConditionTrue == c.Status {
							break Loop
						}
					}
				}
			}
		case <-time.After(5 * time.Second):
			c.Fatalf("Timeout waiting for CRD %v to be established", name)
		}
	}
}

func waitForCRDDeleted(name string, crdWatch watch.Interface, crdClient crdclient.CustomResourceDefinitionInterface, c *check.C) {
Loop:
	for {
		select {
		case watchEvent := <-crdWatch.ResultChan():
			if watch.Deleted == watchEvent.Type {
				if crd, ok := watchEvent.Object.(*apiextensionsv1beta1.CustomResourceDefinition); ok && crd.Name == name {
					break Loop
				}
			}
		case <-time.After(5 * time.Second):
			c.Fatalf("missing watch event for delete event for %v", name)
		}
	}
}

func newCRD(fullName, name, plural, group, kind, version string, scope apiextensionsv1beta1.ResourceScope) *apiextensionsv1beta1.CustomResourceDefinition {
	return &apiextensionsv1beta1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: fullName,
		},
		Spec: apiextensionsv1beta1.CustomResourceDefinitionSpec{
			Group:   group,
			Version: version,
			Names: apiextensionsv1beta1.CustomResourceDefinitionNames{
				Plural:   plural,
				Singular: name,
				Kind:     kind,
			},
			Scope: scope,
		},
	}
}

func clientForSetup(c *check.C) (*clientset.Clientset, *extclient.Clientset, *config.WorkloadContext) {
	mgrConfig := os.Getenv("TEST_CLUSTER_MGR_CONFIG")
	clusterKubeConfig, err := clientcmd.BuildConfigFromFlags("", mgrConfig)
	c.Assert(err, check.IsNil)

	extensionClient, err := extclient.NewForConfig(clusterKubeConfig)
	c.Assert(err, check.IsNil)

	conf := os.Getenv("TEST_CLUSTER_CONFIG")
	workloadKubeConfig, err := clientcmd.BuildConfigFromFlags("", conf)
	c.Assert(err, check.IsNil)

	clusterClient, err := clientset.NewForConfig(workloadKubeConfig)
	c.Assert(err, check.IsNil)

	workload, err := config.NewWorkloadContext(*clusterKubeConfig, *workloadKubeConfig, "")
	c.Assert(err, check.IsNil)

	return clusterClient, extensionClient, workload
}

/*
func newCR(namespace, name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "mygroup.example.com/v1beta1",
			"kind":       "WishIHadChosenNoxu",
			"metadata": map[string]interface{}{
				"namespace": namespace,
				"name":      name,
			},
			"content": map[string]interface{}{
				"key": "value",
			},
			"num": map[string]interface{}{
				"num1": noxuInstanceNum,
				"num2": 1000000,
			},
		},
	}
}
*/
