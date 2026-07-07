package runtimeagent

import "encoding/json"

func cloneRuntime(runtime Runtime) Runtime {
	data, err := json.Marshal(runtime)
	if err != nil {
		return runtime
	}
	var out Runtime
	if err := json.Unmarshal(data, &out); err != nil {
		return runtime
	}
	return out
}

func cloneRuntimeStatus(status RuntimeStatus) RuntimeStatus {
	data, err := json.Marshal(status)
	if err != nil {
		return status
	}
	var out RuntimeStatus
	if err := json.Unmarshal(data, &out); err != nil {
		return status
	}
	return out
}
