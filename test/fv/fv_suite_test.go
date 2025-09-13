/*
Copyright 2024. projectsveltos.io. All rights reserved.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package fv_test

import (
	"context"
	"encoding/base64"
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TwiN/go-color"
	ginkgotypes "github.com/onsi/ginkgo/v2/types"
	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
	"k8s.io/klog/v2/textlogger"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta1" //nolint:staticcheck // SA1019: We are unable to update the dependency at this time.
	"sigs.k8s.io/cluster-api/util"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	"github.com/projectsveltos/libsveltos/lib/clusterproxy"
)

var (
	k8sClient           client.Client
	scheme              *runtime.Scheme
	kindWorkloadCluster *clusterv1.Cluster
)

const (
	timeout         = 2 * time.Minute
	pollingInterval = 5 * time.Second
	//nolint: gosec // this is not a secret. It is the postfix of the Kubernetes secret with kubeconfig
	secretPostfix = "-sveltos-kubeconfig"
)

func TestFv(t *testing.T) {
	RegisterFailHandler(Fail)

	suiteConfig, reporterConfig := GinkgoConfiguration()
	reporterConfig.FullTrace = true
	reporterConfig.JSONReport = "out.json"
	report := func(report ginkgotypes.Report) {
		for i := range report.SpecReports {
			specReport := report.SpecReports[i]
			if specReport.State.String() == "skipped" {
				GinkgoWriter.Printf(color.Colorize(color.Blue, fmt.Sprintf("[Skipped]: %s\n", specReport.FullText())))
			}
		}
		for i := range report.SpecReports {
			specReport := report.SpecReports[i]
			if specReport.Failed() {
				GinkgoWriter.Printf(color.Colorize(color.Red, fmt.Sprintf("[Failed]: %s\n", specReport.FullText())))
			}
		}
	}
	ReportAfterSuite("report", report)

	RunSpecs(t, "FV Suite", suiteConfig, reporterConfig)
}

var _ = BeforeSuite(func() {
	restConfig := ctrl.GetConfigOrDie()
	// To get rid of the annoying request.go log
	restConfig.QPS = 100
	restConfig.Burst = 100

	scheme = runtime.NewScheme()

	ctrl.SetLogger(klog.Background())

	Expect(clientgoscheme.AddToScheme(scheme)).To(Succeed())
	Expect(clusterv1.AddToScheme(scheme)).To(Succeed())
	Expect(libsveltosv1beta1.AddToScheme(scheme)).To(Succeed())

	var err error
	k8sClient, err = client.New(restConfig, client.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred())

	clusterList := &clusterv1.ClusterList{}
	listOptions := []client.ListOption{
		client.MatchingLabels(
			map[string]string{clusterv1.ClusterNameLabel: "clusterapi-workload"},
		),
	}

	Expect(k8sClient.List(context.TODO(), clusterList, listOptions...)).To(Succeed())
	Expect(len(clusterList.Items)).To(Equal(1))
	kindWorkloadCluster = &clusterList.Items[0]

	waitForClusterMachineToBeReady()

	// This allows a pod in the management cluster to reach the managed cluster.
	remoteRestConfig := getManagedClusterRestConfig(kindWorkloadCluster)

	// Creates:
	// - ServiceAccount
	// - ClusterRole
	// - ClusterRoleBinding
	// - get a TokenRequest for ServiceAccount
	// - return a Kubeconfig with such TokenRequest
	kubeconfig := generateKubeconfigWithTokenRequest(remoteRestConfig)

	// Register workload cluster as SveltosCluster.
	registerCluster(kindWorkloadCluster.Namespace, kindWorkloadCluster.Name, kubeconfig)
})

func generateKubeconfigWithTokenRequest(remoteRestConfig *rest.Config) string {
	// This client allows ginkgo, running from terminal, to reach the managed cluster
	workloadClient, err := getKindWorkloadClusterClient()
	Expect(err).To(BeNil())

	projectsveltos := "projectsveltos"
	By(fmt.Sprintf("Creating namespace %s", projectsveltos))
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: projectsveltos,
		},
	}
	err = workloadClient.Create(context.TODO(), ns)
	if err != nil {
		Expect(apierrors.IsAlreadyExists(err)).To(BeTrue())
	}

	By(fmt.Sprintf("Creating serviceAccount %s/%s", projectsveltos, projectsveltos))
	serviceAccount := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: projectsveltos,
			Name:      projectsveltos,
		},
	}
	err = workloadClient.Create(context.TODO(), serviceAccount)
	if err != nil {
		Expect(apierrors.IsAlreadyExists(err)).To(BeTrue())
	}

	By(fmt.Sprintf("Creating clusterRole %s", projectsveltos))
	clusterrole := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: projectsveltos,
		},
		Rules: []rbacv1.PolicyRule{
			{
				Verbs:     []string{"*"},
				APIGroups: []string{"*"},
				Resources: []string{"*"},
			},
			{
				Verbs:           []string{"*"},
				NonResourceURLs: []string{"*"},
			},
		},
	}
	err = workloadClient.Create(context.TODO(), clusterrole)
	if err != nil {
		Expect(apierrors.IsAlreadyExists(err)).To(BeTrue())
	}

	By(fmt.Sprintf("Creating clusterRoleBinding %s", projectsveltos))
	clusterrolebinding := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: projectsveltos,
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.SchemeGroupVersion.Group,
			Kind:     "ClusterRole",
			Name:     projectsveltos,
		},
		Subjects: []rbacv1.Subject{
			{
				Namespace: projectsveltos,
				Name:      projectsveltos,
				Kind:      "ServiceAccount",
				APIGroup:  corev1.SchemeGroupVersion.Group,
			},
		},
	}
	err = workloadClient.Create(context.TODO(), clusterrolebinding)
	if err != nil {
		Expect(apierrors.IsAlreadyExists(err)).To(BeTrue())
	}

	tokenRequest := getServiceAccountTokenRequest(projectsveltos, projectsveltos)
	return getKubeconfigFromToken(remoteRestConfig, projectsveltos, projectsveltos, tokenRequest.Token)
}

// getServiceAccountTokenRequest returns token for a serviceaccount
func getServiceAccountTokenRequest(serviceAccountNamespace, serviceAccountName string,
) *authenticationv1.TokenRequestStatus {

	saExpirationInSecond := 365 * 24 * 60 * time.Minute
	expiration := int64(saExpirationInSecond.Seconds())

	treq := &authenticationv1.TokenRequest{
		Spec: authenticationv1.TokenRequestSpec{
			ExpirationSeconds: &expiration,
		},
	}

	workloadClusterRestConfig, err := getKindWorkloadClusterRestConfig()
	Expect(err).To(BeNil())
	clientset, err := kubernetes.NewForConfig(workloadClusterRestConfig)
	Expect(err).To(BeNil())

	By(fmt.Sprintf("Create Token for ServiceAccount %s/%s", serviceAccountNamespace, serviceAccountName))
	var tokenRequest *authenticationv1.TokenRequest
	tokenRequest, err = clientset.CoreV1().ServiceAccounts(serviceAccountNamespace).
		CreateToken(context.TODO(), serviceAccountName, treq, metav1.CreateOptions{})
	Expect(err).To(BeNil())

	return &tokenRequest.Status
}

// getKubeconfigFromToken returns Kubeconfig to access management cluster from token.
func getKubeconfigFromToken(remoteRestConfig *rest.Config, namespace, serviceAccountName, token string) string {
	template := `apiVersion: v1
kind: Config
clusters:
- name: local
  cluster:
    server: %s
    certificate-authority-data: "%s"
users:
- name: %s
  user:
    token: %s
contexts:
- name: sveltos-context
  context:
    cluster: local
    namespace: %s
    user: %s
current-context: sveltos-context`

	data := fmt.Sprintf(template, remoteRestConfig.Host, base64.StdEncoding.EncodeToString(remoteRestConfig.CAData),
		serviceAccountName, token, namespace, serviceAccountName)

	return data
}

func registerCluster(sveltosClusterNamespace, sveltosClusterName, kubeconfig string) {
	createSecretForSveltosClusterWithKubeconfig(sveltosClusterNamespace, sveltosClusterName, kubeconfig)

	createSveltosCluster(sveltosClusterNamespace, sveltosClusterName)
}

// Creates a Secret for SveltosCluster containing Kubeconfig
func createSecretForSveltosClusterWithKubeconfig(sveltosClusterNamespace, sveltosClusterName, kubeconfig string) {
	sveltosSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: sveltosClusterNamespace,
			Name:      sveltosClusterName + secretPostfix,
		},
		Data: map[string][]byte{
			"data": []byte(kubeconfig),
		},
	}
	err := k8sClient.Create(context.TODO(), sveltosSecret)
	if err != nil {
		Expect(apierrors.IsAlreadyExists(err)).To(BeTrue())
	}
}

func createSveltosCluster(sveltosClusterNamespace, sveltosClusterName string) {
	sveltosCluster := &libsveltosv1beta1.SveltosCluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: sveltosClusterNamespace,
			Name:      sveltosClusterName,
		},
		Spec: libsveltosv1beta1.SveltosClusterSpec{
			TokenRequestRenewalOption: &libsveltosv1beta1.TokenRequestRenewalOption{
				RenewTokenRequestInterval: metav1.Duration{Duration: time.Minute},
				TokenDuration:             metav1.Duration{Duration: time.Hour},
			},
			ReadinessChecks: []libsveltosv1beta1.ClusterCheck{
				{
					Name: "worker-nodes",
					ResourceSelectors: []libsveltosv1beta1.ResourceSelector{
						{
							Kind:    "Node",
							Group:   "",
							Version: "v1",
						},
					},
					Condition: `function evaluate()
  hs = {}
  hs.pass = false

  for _, resource in ipairs(resources) do
    if  not (resource.metadata.labels and resource.metadata.labels["node-role.kubernetes.io/control-plane"]) then
      hs.pass = true
    end
  end
  return hs
end`,
				},
			},
		},
	}

	err := k8sClient.Create(context.TODO(), sveltosCluster)
	if err != nil {
		Expect(apierrors.IsAlreadyExists(err)).To(BeTrue())
	}
}

func waitForClusterMachineToBeReady() {
	By(fmt.Sprintf("Wait for machine in cluster %s/%s to be ready", kindWorkloadCluster.Namespace, kindWorkloadCluster.Name))

	Eventually(func() bool {
		machineList := &clusterv1.MachineList{}
		listOptions := []client.ListOption{
			client.InNamespace(kindWorkloadCluster.Namespace),
			client.MatchingLabels{clusterv1.ClusterNameLabel: kindWorkloadCluster.Name},
		}
		err := k8sClient.List(context.TODO(), machineList, listOptions...)
		if err != nil {
			return false
		}
		for i := range machineList.Items {
			m := machineList.Items[i]
			if m.Status.Phase == string(clusterv1.MachinePhaseRunning) {
				return true
			}
		}
		return false
	}, timeout, pollingInterval).Should(BeTrue())
}

func getManagedClusterRestConfig(workloadCluster *clusterv1.Cluster) *rest.Config {
	logger := textlogger.NewLogger(textlogger.NewConfig(textlogger.Verbosity(1)))
	remoteRestConfig, err := clusterproxy.GetKubernetesRestConfig(context.TODO(), k8sClient,
		workloadCluster.Namespace, workloadCluster.Name, "", "", libsveltosv1beta1.ClusterTypeCapi,
		logger)
	Expect(err).To(BeNil())
	return remoteRestConfig
}

// getKindWorkloadClusterClient returns client to access the kind cluster used as workload cluster
func getKindWorkloadClusterClient() (client.Client, error) {
	restConfig, err := getKindWorkloadClusterRestConfig()
	if err != nil {
		return nil, err
	}
	return client.New(restConfig, client.Options{Scheme: scheme})
}

// getKindWorkloadClusterRestConfig returns restConfig to access the kind cluster used as workload cluster
func getKindWorkloadClusterRestConfig() (*rest.Config, error) {
	kubeconfigPath := "workload_kubeconfig" // this file is created in this directory by Makefile during cluster creation
	config, err := clientcmd.LoadFromFile(kubeconfigPath)
	if err != nil {
		return nil, err
	}
	return clientcmd.NewDefaultClientConfig(*config, &clientcmd.ConfigOverrides{}).ClientConfig()
}

func randomString() string {
	const length = 10
	return util.RandomString(length)
}
