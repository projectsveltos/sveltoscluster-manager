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
	"k8s.io/klog/v2"
	"k8s.io/klog/v2/textlogger"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	libsveltosv1alpha1 "github.com/projectsveltos/libsveltos/api/v1alpha1"
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
	Expect(libsveltosv1alpha1.AddToScheme(scheme)).To(Succeed())

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
	remoteClient, err := client.New(remoteRestConfig, client.Options{Scheme: scheme})
	Expect(err).To(BeNil())

	projectsveltos := "projectsveltos"
	By(fmt.Sprintf("Creating namespace %s", projectsveltos))
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: projectsveltos,
		},
	}
	err = remoteClient.Create(context.TODO(), ns)
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
	err = remoteClient.Create(context.TODO(), serviceAccount)
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
	err = remoteClient.Create(context.TODO(), clusterrole)
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
	err = remoteClient.Create(context.TODO(), clusterrolebinding)
	if err != nil {
		Expect(apierrors.IsAlreadyExists(err)).To(BeTrue())
	}

	tokenRequest := getServiceAccountTokenRequest(remoteRestConfig, projectsveltos, projectsveltos)
	return getKubeconfigFromToken(remoteRestConfig, projectsveltos, projectsveltos, tokenRequest.Token)
}

// getServiceAccountTokenRequest returns token for a serviceaccount
func getServiceAccountTokenRequest(restConfig *rest.Config,
	serviceAccountNamespace, serviceAccountName string) *authenticationv1.TokenRequestStatus {

	saExpirationInSecond := 365 * 24 * 60 * time.Minute
	expiration := int64(saExpirationInSecond.Seconds())

	treq := &authenticationv1.TokenRequest{
		Spec: authenticationv1.TokenRequestSpec{
			ExpirationSeconds: &expiration,
		},
	}

	clientset, err := kubernetes.NewForConfig(restConfig)
	Expect(err).To(BeNil())

	By(fmt.Sprintf("Create Token for ServiceAccount %s/%s", serviceAccountNamespace, serviceAccountName))
	var tokenRequest *authenticationv1.TokenRequest
	tokenRequest, err = clientset.CoreV1().ServiceAccounts(serviceAccountNamespace).
		CreateToken(context.TODO(), serviceAccountName, treq, metav1.CreateOptions{})
	Expect(err).To(BeNil())

	return &tokenRequest.Status
}

// getKubeconfigFromToken returns Kubeconfig to access management cluster from token.
func getKubeconfigFromToken(restConfig *rest.Config, namespace, serviceAccountName, token string) string {
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

	data := fmt.Sprintf(template, restConfig.Host, base64.StdEncoding.EncodeToString(restConfig.CAData),
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
	sveltosCluster := &libsveltosv1alpha1.SveltosCluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: sveltosClusterNamespace,
			Name:      sveltosClusterName,
		},
		Spec: libsveltosv1alpha1.SveltosClusterSpec{
			TokenRequestRenewalOption: &libsveltosv1alpha1.TokenRequestRenewalOption{
				RenewTokenRequestInterval: metav1.Duration{Duration: time.Minute},
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
		workloadCluster.Namespace, workloadCluster.Name, "", "", libsveltosv1alpha1.ClusterTypeCapi,
		logger)
	Expect(err).To(BeNil())
	return remoteRestConfig
}
