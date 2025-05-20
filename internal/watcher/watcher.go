package watcher

import (
	"context"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

const (
	// MachineConfigStateAnnotation is the annotation key used by OpenShift to track node state
	MachineConfigStateAnnotation = "machineconfiguration.openshift.io/state"
	// MachineConfigStateWorking indicates the node is being updated
	MachineConfigStateWorking = "Working"
	// MachineConfigStateDone indicates the node update is complete
	MachineConfigStateDone = "Done"
)

type NodeState struct {
	Name      string
	IsRolling bool
}

type Watcher struct {
	client  kubernetes.Interface
	stateCh chan NodeState
	// Track previous states to detect changes
	previousStates sync.Map
}

func NewWatcher(client kubernetes.Interface) *Watcher {
	return &Watcher{
		client:  client,
		stateCh: make(chan NodeState, 10),
	}
}

func (w *Watcher) Start(ctx context.Context) {
	go w.watchNodes(ctx)
}

func (w *Watcher) StateChannel() <-chan NodeState {
	return w.stateCh
}

func (w *Watcher) watchNodes(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			nodes, err := w.client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
			if err != nil {
				klog.Errorf("Failed to list nodes: %v", err)
				continue
			}

			for _, node := range nodes.Items {
				state, exists := node.Annotations[MachineConfigStateAnnotation]
				// TODO: also consider another annotation used for manual node reboots
				isTainted := containTaint(node.Spec.Taints, "wait-for-runc")

				// if machine-config is working or tainted , it's rolling
				isRolling := (exists && state == MachineConfigStateWorking) || (isTainted)

				// Get previous state with type-safe handling
				prevState, _ := w.previousStates.LoadOrStore(node.Name, false)
				wasRolling, ok := prevState.(bool)
				if !ok {
					wasRolling = false
					klog.Warningf("Invalid state type for node %s, resetting to false", node.Name)
				}

				// Only send state changes
				if isRolling != wasRolling {
					w.previousStates.Store(node.Name, isRolling)
					w.stateCh <- NodeState{
						Name:      node.Name,
						IsRolling: isRolling,
					}
					klog.Infof("Node %s state changed: rolling=%v", node.Name, isRolling)

					// no longer need to track
					if !isRolling {
						w.previousStates.Delete(node.Name)
					}
				}
			}
		}
	}
}

func containTaint(taints []corev1.Taint, taintName string) bool {
	for _, t := range taints {
		if t.Key == taintName {
			return true
		}
	}
	return false
}
