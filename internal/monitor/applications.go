package monitor

import (
	"encoding/json"

	collectorv1 "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortracev1 "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"
)

func observedMetricServices(body []byte, jsonMode bool) []string {
	seen := map[string]bool{}
	if jsonMode {
		var payload otlpPayload
		if json.Unmarshal(body, &payload) != nil {
			return nil
		}
		for _, resource := range payload.ResourceMetrics {
			service, _, _ := jsonResourceIdentity(resource.Resource.Attributes)
			if service != "" && service != "unknown" {
				seen[service] = true
			}
		}
	} else {
		var request collectorv1.ExportMetricsServiceRequest
		if proto.Unmarshal(body, &request) != nil {
			return nil
		}
		for _, resource := range request.ResourceMetrics {
			if resource.GetResource() == nil {
				continue
			}
			service, _, _ := protoResourceIdentity(resource.GetResource().GetAttributes())
			if service != "" && service != "unknown" {
				seen[service] = true
			}
		}
	}
	return stringKeys(seen)
}

func observedTraceServices(body []byte) []string {
	var request collectortracev1.ExportTraceServiceRequest
	if proto.Unmarshal(body, &request) != nil {
		return nil
	}
	seen := map[string]bool{}
	for _, resource := range request.ResourceSpans {
		if resource.GetResource() == nil {
			continue
		}
		service, _, _ := protoResourceIdentity(resource.GetResource().GetAttributes())
		if service != "" && service != "unknown" {
			seen[service] = true
		}
	}
	return stringKeys(seen)
}

func stringKeys(values map[string]bool) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	return out
}
