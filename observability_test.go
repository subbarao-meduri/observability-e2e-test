package main_test

import (
	"errors"
	"strings"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/open-cluster-management/observability-e2e-test/utils"
)

const (
	MCO_OPERATOR_NAMESPACE = "open-cluster-management"
	MCO_CR_NAME            = "observability"
	MCO_NAMESPACE          = "open-cluster-management-observability"
	MCO_ADDON_NAMESPACE    = "open-cluster-management-addon-observability"
	MCO_LABEL              = "name=multicluster-observability-operator"
)

var (
	EventuallyTimeoutMinute  time.Duration = 60 * time.Second
	EventuallyIntervalSecond time.Duration = 1 * time.Second

	hubClient kubernetes.Interface
	dynClient dynamic.Interface
)

var _ = Describe("Observability", func() {
	BeforeEach(func() {
		hubClient = utils.NewKubeClient(
			testOptions.HubCluster.MasterURL,
			testOptions.KubeConfig,
			testOptions.HubCluster.KubeContext)

		dynClient = utils.NewKubeClientDynamic(
			testOptions.HubCluster.MasterURL,
			testOptions.KubeConfig,
			testOptions.HubCluster.KubeContext)
	})

	It("Observability: MCO Operator is created", func() {
		var podList, _ = hubClient.CoreV1().Pods(MCO_OPERATOR_NAMESPACE).List(metav1.ListOptions{LabelSelector: MCO_LABEL})
		Expect(len(podList.Items)).To(Equal(1))
		for _, pod := range podList.Items {
			Expect(string(pod.Status.Phase)).To(Equal("Running"))
		}
	})

	It("Observability: Required CRDs are created", func() {
		Eventually(func() error {
			return utils.HaveCRDs(testOptions.HubCluster, testOptions.KubeConfig,
				[]string{
					"multiclusterobservabilities.observability.open-cluster-management.io",
					"observatoria.core.observatorium.io",
					"observabilityaddons.observability.open-cluster-management.io",
				})
		}).Should(Succeed())
	})

	It("Observability: All required components are deployed and running", func() {
		Expect(utils.CreateMCONamespace(testOptions)).NotTo(HaveOccurred())
		Expect(utils.CreatePullSecret(testOptions)).NotTo(HaveOccurred())
		Expect(utils.CreateObjSecret(testOptions)).NotTo(HaveOccurred())

		By("Creating MCO instance")
		mco := utils.NewMCOInstanceYaml(MCO_CR_NAME)
		Expect(utils.Apply(testOptions.HubCluster.MasterURL, testOptions.KubeConfig, testOptions.HubCluster.KubeContext, mco)).NotTo(HaveOccurred())

		By("Waiting for MCO ready status")
		Eventually(func() bool {
			instance, err := dynClient.Resource(utils.NewMCOGVR()).Get(MCO_CR_NAME, metav1.GetOptions{})
			if err == nil {
				return utils.StatusContainsTypeEqualTo(instance, "Ready")
			}
			return false
		}, EventuallyTimeoutMinute*5, EventuallyIntervalSecond*5).Should(BeTrue())
	})

	It("Observability: Grafana console can be accessible", func() {
		Eventually(func() error {
			err := utils.CheckGrafanaConsole(testOptions)
			if err != nil {
				return err
			}
			return nil
		}, EventuallyTimeoutMinute*5, EventuallyIntervalSecond*5).Should(Succeed())
	})

	It("Observability: retentionResolutionRaw is modified", func() {
		By("Modifying MCO retentionResolutionRaw filed")
		err := utils.ModifyMCORetentionResolutionRaw(testOptions)
		Expect(err).ToNot(HaveOccurred())

		By("Waiting for MCO retentionResolutionRaw filed to take effect")
		Eventually(func() error {
			name := MCO_CR_NAME + "-observatorium-thanos-compact"
			compact, getError := hubClient.AppsV1().StatefulSets(MCO_NAMESPACE).Get(name, metav1.GetOptions{})
			if getError != nil {
				return getError
			}
			argList := compact.Spec.Template.Spec.Containers[0].Args
			for _, arg := range argList {
				if arg == "--retention.resolution-raw=3d" {
					return nil
				}
			}
			return errors.New("Failed to find modified retention field")
		}, EventuallyTimeoutMinute*5, EventuallyIntervalSecond*5).Should(Succeed())
	})

	It("Observability: Managed cluster metrics shows up in Grafana console", func() {
		Eventually(func() error {
			err, _ := utils.ContainManagedClusterMetric(testOptions)
			return err
		}, EventuallyTimeoutMinute*5, EventuallyIntervalSecond*5).Should(Succeed())
	})

	It("Observability: disable observabilityaddon", func() {
		By("Modifying MCO cr to disable observabilityaddon")
		err := utils.ModifyMCOobservabilityAddonSpec(testOptions)
		Expect(err).ToNot(HaveOccurred())

		By("Waiting for MCO addon components scales to 0")
		Eventually(func() error {
			addonLabel := "component=metrics-collector"
			var podList, _ = hubClient.CoreV1().Pods(MCO_ADDON_NAMESPACE).List(metav1.ListOptions{LabelSelector: addonLabel})
			if len(podList.Items) != 0 {
				return errors.New("Failed to disable observability addon")
			}
			return nil
		}, EventuallyTimeoutMinute*10, EventuallyIntervalSecond*5).Should(Succeed())

		By("Waiting for check no metric data in grafana console")
		Eventually(func() error {
			err, hasMetric := utils.ContainManagedClusterMetric(testOptions)
			if err != nil && !hasMetric && strings.Contains(err.Error(), "Failed to find metric name from response") {
				return nil
			}
			return errors.New("Found metric data in grafana console")
		}, EventuallyTimeoutMinute*10, EventuallyIntervalSecond*5).Should(Succeed())
	})

	It("Observability: Modify availabilityConfig from High to Basic", func() {
		By("Modifying MCO availabilityConfig filed")
		err := utils.ModifyMCOAvailabilityConfig(testOptions)
		Expect(err).ToNot(HaveOccurred())

		By("Checking MCO components in Basic mode")
		Eventually(func() error {
			err = utils.CheckMCOComponentsInBaiscMode(testOptions)

			if err != nil {
				return err
			}
			return nil
		}, EventuallyTimeoutMinute*5, EventuallyIntervalSecond*5).Should(Succeed())
	})

	It("Observability: Clean up", func() {
		By("Uninstall MCO instance")
		err := utils.UninstallMCO(testOptions)
		Expect(err).ToNot(HaveOccurred())

		By("Waiting for delete all MCO components")
		Eventually(func() error {
			var podList, _ = hubClient.CoreV1().Pods(MCO_NAMESPACE).List(metav1.ListOptions{})
			if len(podList.Items) != 0 {
				return err
			}
			return nil
		}, EventuallyTimeoutMinute*5, EventuallyIntervalSecond*5).Should(Succeed())

		By("Waiting for delete MCO addon instance")
		Eventually(func() error {
			gvr := utils.NewMCOAddonGVR()
			name := MCO_CR_NAME + "-addon"
			instance, _ := dynClient.Resource(gvr).Namespace("local-cluster").Get(name, metav1.GetOptions{})
			if instance != nil {
				return errors.New("Failed to delete MCO addon instance")
			}
			return nil
		}, EventuallyTimeoutMinute*5, EventuallyIntervalSecond*5).Should(Succeed())

		By("Waiting for delete all MCO addon components")
		Eventually(func() error {
			var podList, _ = hubClient.CoreV1().Pods(MCO_ADDON_NAMESPACE).List(metav1.ListOptions{})
			if len(podList.Items) != 0 {
				return err
			}
			return nil
		}, EventuallyTimeoutMinute*5, EventuallyIntervalSecond*5).Should(Succeed())

		By("Waiting for delete MCO namespaces")
		Eventually(func() error {
			err := hubClient.CoreV1().Namespaces().Delete(MCO_NAMESPACE, &metav1.DeleteOptions{})
			if err != nil {
				return err
			}
			return nil
		}, EventuallyTimeoutMinute*5, EventuallyIntervalSecond*5).Should(Succeed())
	})
})