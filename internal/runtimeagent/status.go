package runtimeagent

import (
	"strconv"
	"strings"
	"time"
)

func NewStatus(runtime Runtime, phase string, endpoints map[string][]Endpoint, err error) RuntimeStatus {
	return NewStatusWithOptions(runtime, phase, endpoints, err, StatusOptions{})
}

type StatusOptions struct {
	NodeName           string
	DeploymentRestarts map[string]int
	DeploymentMessages map[string]string
}

func NewStatusWithOptions(runtime Runtime, phase string, endpoints map[string][]Endpoint, err error, options StatusOptions) RuntimeStatus {
	runtime = NormalizeRuntime(runtime)
	if endpoints == nil {
		endpoints = map[string][]Endpoint{}
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	readyReplicas := readyReplicasByDeployment(runtime, endpoints)
	if phase == "" {
		phase = RuntimePhaseRunning
	}
	if err != nil {
		phase = RuntimePhaseFailed
	} else if phase == RuntimePhaseRunning && !allDeploymentsReady(runtime, readyReplicas) {
		phase = RuntimePhaseDegraded
	}
	nodeName := strings.TrimSpace(options.NodeName)
	if nodeName == "" {
		nodeName = strings.TrimSpace(runtime.Spec.NodeName)
	}
	status := RuntimeStatus{
		ObservedGeneration: runtime.Metadata.Generation,
		Phase:              phase,
		NodeName:           nodeName,
		LastTransitionTime: now,
	}
	status.Conditions = runtimeConditions(runtime, status.Phase, readyReplicas, endpoints, err, now, nodeName)
	resourceMessage := ""
	if err != nil {
		resourceMessage = err.Error()
	}
	for _, deployment := range runtime.Spec.Deployments {
		deploymentPhase := resourcePhaseForDeployment(status.Phase, deployment, readyReplicas[deployment.Name], err)
		message := resourceMessage
		if message == "" {
			message = options.DeploymentMessages[deployment.Name]
		}
		status.Deployments = append(status.Deployments, DeploymentStatus{
			Name:     deployment.Name,
			Ready:    readyReplicas[deployment.Name],
			Replicas: deploymentReplicas(deployment),
			Image:    deployment.Image,
			Revision: deployment.Revision,
			Restarts: options.DeploymentRestarts[deployment.Name],
			Phase:    deploymentPhase,
			Message:  message,
		})
	}
	for _, service := range runtime.Spec.Services {
		resolved := service.TargetPort.String()
		if port, resolveErr := resolveServiceTargetPort(service, matchingDeployments(runtime, service.Selector)); resolveErr == nil {
			resolved = strconv.Itoa(port)
		}
		copied := append([]Endpoint(nil), endpoints[service.Name]...)
		status.Services = append(status.Services, ServiceStatus{
			Name:       service.Name,
			Port:       service.Port,
			ListenPort: service.ListenPort,
			TargetPort: resolved,
			Endpoints:  copied,
			Phase:      resourcePhaseForService(runtime, service, status.Phase, endpoints, err),
		})
	}
	return fixIngressStatus(status, runtime, endpoints, err)
}

func runtimeConditions(runtime Runtime, phase string, readyReplicas map[string]int, endpoints map[string][]Endpoint, err error, now, nodeName string) []Condition {
	conditions := []Condition{
		newCondition("SpecAccepted", ConditionTrue, "Validated", "", now),
	}
	if nodeName == "" {
		conditions = append(conditions, newCondition("NodeAssigned", ConditionUnknown, "PendingAssignment", "runtime has not been assigned to a node", now))
	} else {
		conditions = append(conditions, newCondition("NodeAssigned", ConditionTrue, "Assigned", "runtime assigned to node "+nodeName, now))
	}
	if err != nil {
		message := err.Error()
		return append(conditions,
			newCondition("ImagePulled", ConditionFalse, "ReconcileFailed", message, now),
			newCondition("ContainerReady", ConditionFalse, "ReconcileFailed", message, now),
			newCondition("ServicesReady", ConditionFalse, "ReconcileFailed", message, now),
			newCondition("IngressReady", ConditionFalse, "ReconcileFailed", message, now),
		)
	}
	switch phase {
	case RuntimePhasePending:
		conditions = append(conditions,
			newCondition("ImagePulled", ConditionUnknown, "Pending", "runtime reconciliation has not started image pulls", now),
			newCondition("ContainerReady", ConditionUnknown, "Pending", "containers have not started yet", now),
		)
	case RuntimePhasePulling:
		conditions = append(conditions,
			newCondition("ImagePulled", ConditionUnknown, "Pulling", "runtime is pulling deployment images", now),
			newCondition("ContainerReady", ConditionUnknown, "Pending", "containers are waiting for images", now),
		)
	case RuntimePhaseStarting, RuntimePhaseUpdating:
		conditions = append(conditions,
			newCondition("ImagePulled", ConditionTrue, "Pulled", "", now),
			newCondition("ContainerReady", ConditionUnknown, phase, "containers are converging to desired state", now),
		)
	default:
		if allDeploymentsReady(runtime, readyReplicas) {
			conditions = append(conditions, newCondition("ImagePulled", ConditionTrue, "Pulled", "", now))
			conditions = append(conditions, newCondition("ContainerReady", ConditionTrue, "Ready", "", now))
		} else {
			conditions = append(conditions, newCondition("ImagePulled", ConditionTrue, "Pulled", "", now))
			conditions = append(conditions, newCondition("ContainerReady", ConditionFalse, "WaitingForReadyReplicas", "one or more deployment replicas are not ready", now))
		}
	}
	if allServicesReady(runtime, endpoints) {
		conditions = append(conditions, newCondition("ServicesReady", ConditionTrue, "ProxyConfigured", "", now))
	} else {
		conditions = append(conditions, newCondition("ServicesReady", ConditionFalse, "NoReadyEndpoints", "one or more services have no ready endpoints", now))
	}
	if allIngressReady(runtime, endpoints) {
		conditions = append(conditions, newCondition("IngressReady", ConditionTrue, "ProxyConfigured", "", now))
	} else {
		conditions = append(conditions, newCondition("IngressReady", ConditionFalse, "ServiceNotReady", "one or more ingress routes target services without ready endpoints", now))
	}
	return conditions
}

func newCondition(conditionType, status, reason, message, now string) Condition {
	return Condition{
		Type:               conditionType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: now,
	}
}

func resourcePhaseForDeployment(runtimePhase string, deployment DeploymentSpec, ready int, err error) string {
	if err != nil {
		return RuntimePhaseFailed
	}
	switch runtimePhase {
	case RuntimePhasePending, RuntimePhasePulling, RuntimePhaseStarting, RuntimePhaseUpdating, RuntimePhaseTerminating:
		return runtimePhase
	}
	if ready >= deploymentReplicas(deployment) {
		return RuntimePhaseRunning
	}
	return RuntimePhaseDegraded
}

func resourcePhaseForService(runtime Runtime, service ServiceSpec, runtimePhase string, endpoints map[string][]Endpoint, err error) string {
	if err != nil {
		return RuntimePhaseFailed
	}
	switch runtimePhase {
	case RuntimePhasePending, RuntimePhasePulling, RuntimePhaseStarting, RuntimePhaseUpdating, RuntimePhaseTerminating:
		return runtimePhase
	}
	if serviceReady(runtime, service, endpoints) {
		return RuntimePhaseRunning
	}
	return RuntimePhaseDegraded
}

func readyReplicasByDeployment(runtime Runtime, endpoints map[string][]Endpoint) map[string]int {
	ready := map[string]int{}
	seen := map[string]bool{}
	for _, service := range runtime.Spec.Services {
		matches := matchingDeployments(runtime, service.Selector)
		for _, endpoint := range endpoints[service.Name] {
			for _, deployment := range matches {
				for replica := 1; replica <= deploymentReplicas(deployment); replica++ {
					name := containerNameForDeployment(runtime, deployment, replica)
					if endpoint.Container != name || seen[name] {
						continue
					}
					ready[deployment.Name]++
					seen[name] = true
				}
			}
		}
	}
	return ready
}

func allDeploymentsReady(runtime Runtime, readyReplicas map[string]int) bool {
	for _, deployment := range runtime.Spec.Deployments {
		if readyReplicas[deployment.Name] < deploymentReplicas(deployment) {
			return false
		}
	}
	return true
}

func allServicesReady(runtime Runtime, endpoints map[string][]Endpoint) bool {
	for _, service := range runtime.Spec.Services {
		if !serviceReady(runtime, service, endpoints) {
			return false
		}
	}
	return true
}

func serviceReady(runtime Runtime, service ServiceSpec, endpoints map[string][]Endpoint) bool {
	desired := 0
	for _, deployment := range matchingDeployments(runtime, service.Selector) {
		desired += deploymentReplicas(deployment)
	}
	if desired == 0 {
		return true
	}
	return len(endpoints[service.Name]) >= desired
}

func allIngressReady(runtime Runtime, endpoints map[string][]Endpoint) bool {
	for _, ingress := range runtime.Spec.Ingress {
		for _, route := range ingress.Routes {
			service, ok := serviceByName(runtime, route.ServiceName)
			if !ok || !serviceReady(runtime, service, endpoints) {
				return false
			}
		}
	}
	return true
}

func fixIngressStatus(status RuntimeStatus, runtime Runtime, endpoints map[string][]Endpoint, err error) RuntimeStatus {
	for _, ingress := range runtime.Spec.Ingress {
		phase := status.Phase
		if err != nil {
			phase = RuntimePhaseFailed
		} else {
			switch status.Phase {
			case RuntimePhasePending, RuntimePhasePulling, RuntimePhaseStarting, RuntimePhaseUpdating, RuntimePhaseTerminating:
			default:
				phase = RuntimePhaseRunning
				for _, route := range ingress.Routes {
					service, ok := serviceByName(runtime, route.ServiceName)
					if !ok || !serviceReady(runtime, service, endpoints) {
						phase = RuntimePhaseDegraded
						break
					}
				}
			}
		}
		status.Ingress = append(status.Ingress, IngressStatus{
			Name:       ingress.Name,
			Host:       ingress.Host,
			ListenPort: ingress.ListenPort,
			Phase:      phase,
		})
	}
	return status
}
