package statefuljob

import (
	"github.com/strangelove-ventures/cosmos-operator/internal/kube"
)

func defaultLabels() map[string]string {
	return map[string]string{
		kube.ControllerLabel: "cosmos-operator",
		kube.ComponentLabel:  "StatefulJob",
	}
}
