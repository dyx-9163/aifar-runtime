package runtimeagent

import (
	"strconv"
	"time"
)

func NewStatus(runtime Runtime, phase string, endpoints map[string][]Endpoint, err error) RuntimeStatus {
	runtime = NormalizeRuntime(runtime)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	status := RuntimeStatus{
		ObservedGeneration: runtime.Metadata.Generation,
		Phase:              phase,
		LastTransitionTime: now,
	}
	if status.Phase == "" {
		status.Phase = "Ready"
	}
	status.Conditions = []Condition{
		{Type: "SpecAccepted", Status: "True", Reason: "Validated", LastTransitionTime: now},
	}
	resourcePhase := "Ready"
	resourceMessage := ""
	if err != nil {
		status.Phase = "Failed"
		resourcePhase = "Failed"
		resourceMessage = err.Error()
		status.Conditions = append(status.Conditions,
			Condition{Type: "DeploymentsReady", Status: "False", Reason: "ReconcileFailed", Message: err.Error(), LastTransitionTime: now},
			Condition{Type: "ServicesReady", Status: "False", Reason: "ReconcileFailed", Message: err.Error(), LastTransitionTime: now},
			Condition{Type: "IngressReady", Status: "False", Reason: "ReconcileFailed", Message: err.Error(), LastTransitionTime: now},
		)
	} else {
		status.Conditions = append(status.Conditions,
			Condition{Type: "DeploymentsReady", Status: "True", Reason: "Reconciled", LastTransitionTime: now},
			Condition{Type: "ServicesReady", Status: "True", Reason: "ProxyConfigured", LastTransitionTime: now},
			Condition{Type: "IngressReady", Status: "True", Reason: "ProxyConfigured", LastTransitionTime: now},
		)
	}
	readyReplicas := readyReplicasByDeployment(runtime, endpoints)
	for _, deployment := range runtime.Spec.Deployments {
		status.Deployments = append(status.Deployments, DeploymentStatus{
			Name:     deployment.Name,
			Ready:    readyReplicas[deployment.Name],
			Replicas: deploymentReplicas(deployment),
			Image:    deployment.Image,
			Revision: deployment.Revision,
			Phase:    resourcePhase,
			Message:  resourceMessage,
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
			Phase:      resourcePhase,
		})
	}
	return fixIngressStatus(status, runtime, resourcePhase)
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

func fixIngressStatus(status RuntimeStatus, runtime Runtime, phase string) RuntimeStatus {
	for _, ingress := range runtime.Spec.Ingress {
		status.Ingress = append(status.Ingress, IngressStatus{
			Name:       ingress.Name,
			Host:       ingress.Host,
			ListenPort: ingress.ListenPort,
			Phase:      phase,
		})
	}
	return status
}
