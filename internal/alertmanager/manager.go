package alertmanager

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/alertmanager/api/v2/models"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

type SilenceManager struct {
	amClient       *Client
	activeSilences sync.Map
	k8sClient      kubernetes.Interface
}

func NewSilenceManager(client *Client, k8sClient kubernetes.Interface) *SilenceManager {
	manager := &SilenceManager{
		amClient:  client,
		k8sClient: k8sClient,
	}

	// Load existing silences
	ctx := context.Background()
	silences, err := client.GetSilences(ctx)
	if err != nil {
		klog.Warningf("Failed to load existing silences: %v", err)
	} else {
		for _, silence := range silences {
			// Store silences created by rollout-helper
			if silence.CreatedBy != nil && *silence.CreatedBy == "rollout-helper" {

				// Delete alert if expired
				if silence.EndsAt != nil && time.Now().After(time.Time(*silence.EndsAt)) {
					if err := client.DeleteSilenceID(ctx, silence.ID); err != nil {
						klog.Errorf("Failed to delete expired silence %s: %v", silence.ID, err)
					} else {
						klog.Infof("Deleted expired silence %s", silence.ID)
					}
					continue
				}

				// Load alert if not expired
				for _, matcher := range silence.Matchers {
					if matcher.Name != nil && *matcher.Name == "node" && matcher.Value != nil {
						manager.activeSilences.Store(*matcher.Value, true)
						klog.Infof("Loaded existing silence for node %s", *matcher.Value)
					}
				}
			}
		}
	}

	return manager
}

func (m *SilenceManager) HandleNodeState(ctx context.Context, nodeName string, isRolling bool) error {
	if isRolling {
		_, exist := m.activeSilences.Load(nodeName)
		if exist {
			klog.Infof("Alert already exist for Node %s: Ignoring", nodeName)
			return nil
		}

		// Create silence when node starts rolling
		m.CreateNodeSilence(ctx, nodeName)
		m.CreateInstanceSilence(ctx, nodeName)
		m.CreatePodSilence(ctx, nodeName)

		m.activeSilences.Store(nodeName, true)
		klog.Infof("Created silence for node %s", nodeName)
	} else {
		// Remove silence when node is done rolling
		if _, exists := m.activeSilences.LoadAndDelete(nodeName); exists {
			if err := m.amClient.DeleteSilence(ctx, nodeName); err != nil {
				return fmt.Errorf("failed to delete silence for node %s: %w", nodeName, err)
			}
			klog.Infof("Removed silence for node %s", nodeName)
		}
	}
	return nil
}

type daemonSetIdent struct {
	namespace string
	dsName    string
	label     string
}

func (m *SilenceManager) CreatePodSilence(ctx context.Context, nodeName string) error {
	dsList := []daemonSetIdent{
		{ // CiliumScrapingTargetDown
			"kube-system",
			"cilium",
			"k8s-app=cilium",
		},
		{ // DnsScrapingTargetDown
			"openshift-dns",
			"dns",
			"app=openshift-dns",
		},
		{ // ScrapingTargetDown collector
			"openshift-logging",
			"collector",
			"component=collector",
		},
		{ // ScrapingTargetDown fluent-bit
			"snappcloud-logging",
			"flunet-bit",
			"app.kubernetes.io/name=fluentbit",
		},
	}

	// Collect all pod names and namespaces
	var podNames []string
	var namespaces []string

	for _, dsIdent := range dsList {
		// List pods for this daemonset on the specified node
		pods, err := m.k8sClient.CoreV1().Pods(dsIdent.namespace).List(ctx, metav1.ListOptions{
			FieldSelector: fmt.Sprintf("spec.nodeName=%s", nodeName),
			LabelSelector: dsIdent.label,
		})
		if err != nil {
			klog.Errorf("Failed to list pods for daemonset %s/%s: %v", dsIdent.namespace, dsIdent.dsName, err)
			continue
		}

		for _, pod := range pods.Items {
			podNames = append(podNames, pod.Name)
			namespaces = append(namespaces, pod.Namespace)
		}
	}

	if len(podNames) == 0 {
		klog.Infof("No pods found for node %s", nodeName)
		return nil
	}

	// Create a single silence for all pods
	matchers := models.Matchers{
		{
			Name:    stringPtr("pod"),
			Value:   stringPtr(fmt.Sprintf("(%s)", strings.Join(podNames, "|"))),
			IsRegex: boolPtr(true),
		},
		{
			Name:    stringPtr("namespace"),
			Value:   stringPtr(fmt.Sprintf("(%s)", strings.Join(namespaces, "|"))),
			IsRegex: boolPtr(true),
		},
	}

	if err := m.amClient.CreateSilence(ctx, matchers, nodeName); err != nil {
		return fmt.Errorf("failed to create silence for pods: %w", err)
	}

	klog.Infof("Created silence for %d pods on node %s", len(podNames), nodeName)
	return nil
}

func (m *SilenceManager) CreateInstanceSilence(ctx context.Context, nodeName string) error {
	// Define services that need to be silenced
	alertServices := []string{
		"node-exporter",
		"kubernetes-cadvisor",
		"kubelet",
	}

	// Create a single regex pattern that matches all services
	servicesPattern := fmt.Sprintf("(%s)", strings.Join(alertServices, "|"))

	matchers := models.Matchers{
		{
			Name:    stringPtr("instance"),
			Value:   stringPtr(nodeName),
			IsRegex: boolPtr(false),
		},
		{
			Name:    stringPtr("alertname"),
			Value:   stringPtr("ScrapingTargetDown|NodeScrapingTargetDown"),
			IsRegex: boolPtr(true),
		},
		{
			Name:    stringPtr("job"),
			Value:   stringPtr(servicesPattern),
			IsRegex: boolPtr(true),
		},
	}
	if err := m.amClient.CreateSilence(ctx, matchers, nodeName); err != nil {
		klog.Errorf("failed to create silence for instance %s: %w", nodeName, err)
	}
	return nil
}

func (m *SilenceManager) CreateNodeSilence(ctx context.Context, nodeName string) error {
	_, exist := m.activeSilences.Load(nodeName)
	if exist {
		klog.Infof("Alert already exist for Node %s: Ignoring", nodeName)
		return nil
	}

	alertNames := []string{
		"KubeNodeNotReady",
		"KubeNodeUnreachable",
		"NodeScrapingTargetDown",
		"ScrapingTargetDown",
		"EventWarning",
	}

	// Define services that need to be silenced
	alertServices := []string{
		"snappcloud-network-vector\\/spcld-network-vector-agent",
		"event-exporter",
		"node-exporter",
		"kube-state-metrics",
		"crio",
		"kubelet",
	}

	// Create a single regex pattern that matches all services
	alertPattern := fmt.Sprintf("(%s)", strings.Join(alertNames, "|"))
	servicesPattern := fmt.Sprintf("(%s)", strings.Join(alertServices, "|"))

	matchers := models.Matchers{
		{
			Name:    stringPtr("node"),
			Value:   stringPtr(nodeName),
			IsRegex: boolPtr(false),
		},
		{
			Name:    stringPtr("alertname"),
			Value:   stringPtr(alertPattern),
			IsRegex: boolPtr(true),
		},
		{
			Name:    stringPtr("job"),
			Value:   stringPtr(servicesPattern),
			IsRegex: boolPtr(true),
		},
	}
	if err := m.amClient.CreateSilence(ctx, matchers, nodeName); err != nil {
		klog.Errorf("failed to create silence for node %s: %w", nodeName, err)
	}
	return nil
}
