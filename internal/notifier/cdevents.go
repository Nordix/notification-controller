/*
Copyright 2020 The Flux authors

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

package notifier

import (
	"context"
	"crypto/x509"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/hashicorp/go-retryablehttp"

	cdevents "github.com/cdevents/sdk-go/pkg/api"
	eventv1 "github.com/fluxcd/pkg/apis/event/v1beta1"
)

// CDEvents holds the incoming webhook URL
type CDEvents struct {
	URL      string
	ProxyURL string
	CertPool *x509.CertPool
}

func NewCDEvents(hookURL string, proxyURL string, certPool *x509.CertPool) (*CDEvents, error) {
	_, err := url.ParseRequestURI(hookURL)
	if err != nil {
		return nil, fmt.Errorf("invalid webhook URL %s: '%w'", hookURL, err)
	}

	return &CDEvents{
		URL:      hookURL,
		ProxyURL: proxyURL,
		CertPool: certPool,
	}, nil
}

// CDEventsPayload holds the message card data
type CDEventsPayload struct {
	Id     string `json:"@id"`
	Type   string `json:"@type"`
	Source string `json:"@source"`
}

type CDEventsField struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Post CDEvents message
func (s *CDEvents) Post(ctx context.Context, event eventv1.Event) error {
	facts := make([]CDEventsField, 0, len(event.Metadata))
	for k, v := range event.Metadata {
		facts = append(facts, CDEventsField{
			Name:  k,
			Value: v,
		})
	}

	var payload cdevents.CDEvent
	var err1 error

	switch strings.ToLower(event.Reason) {
	case "installsucceeded":
		mapEvent, _ := cdevents.NewEnvironmentModifiedEvent()
		payload = mapEvent
	case "upgradesucceeded":
		mapEvent, _ := cdevents.NewTaskRunFinishedEvent()
		mapEvent.SetSubjectOutcome("Success")
		payload = mapEvent
	case "upgradefailed":
		mapEvent, _ := cdevents.NewTaskRunFinishedEvent()
		mapEvent.SetSubjectOutcome("Failure")
		payload = mapEvent
	case "testsucceeded":
		mapEvent, _ := cdevents.NewTestCaseRunFinishedEvent()
		mapEvent.SetSubjectOutcome("Success")
		payload = mapEvent
	case "testfailed":
		mapEvent, _ := cdevents.NewTestCaseRunFinishedEvent()
		mapEvent.SetSubjectOutcome("Success")
		payload = mapEvent
	case "rollbacksucceeded":
		mapEvent, _ := cdevents.NewTaskRunFinishedEvent()
		mapEvent.SetSubjectOutcome("Success")
		payload = mapEvent
	case "rollbackfailed":
		mapEvent, _ := cdevents.NewTaskRunFinishedEvent()
		mapEvent.SetSubjectOutcome("Failure")
		payload = mapEvent
	case "driftdetected":
		mapEvent, _ := cdevents.NewTaskRunFinishedEvent()
		mapEvent.SetSubjectOutcome("Failure")
		payload = mapEvent
	case "reconciliationsucceeded":
		mapEvent, _ := cdevents.NewServiceDeployedEvent()
		mapEvent.SetSubjectArtifactId(string(event.InvolvedObject.UID))
		var reference cdevents.Reference
		reference.Id = string(event.InvolvedObject.UID)
		reference.Source = event.InvolvedObject.Name
		mapEvent.SetSubjectEnvironment(&reference)
		payload = mapEvent
	default:
		mapEvent, _ := cdevents.NewIncidentDetectedEvent()
		payload = mapEvent
	}

	sourceFormat := fmt.Sprintf("%s.%s", event.InvolvedObject.Name, event.InvolvedObject.Kind)

	payload.SetSource(sourceFormat)
	payload.SetCustomData("application/json", event)
	payload.SetSubjectId(string(event.InvolvedObject.UID))

	fmt.Println("Setting CDEvent Headers")

	// err := postMessage(ctx, s.URL, s.ProxyURL, s.CertPool, payload)
	err := postMessage(ctx, s.URL, s.ProxyURL, s.CertPool, payload, func(request *retryablehttp.Request) {
		request.Header.Add("ce-type", payload.GetType().String())
		request.Header.Add("ce-specversion", "0.3")
		request.Header.Add("ce-source", payload.GetSource())
		request.Header.Add("ce-id", payload.GetId())
		request.Header.Add("ce-time", payload.GetTimestamp().Format(time.RFC3339Nano))
		request.Header.Add("prefer", "reply")

		request.Header.Add("Accept", "application/json")
		request.Header.Add("Content-Type", "application/json; charset=UTF-8")
	})

	if err != nil && err1 != nil {
		return fmt.Errorf("postMessage failed: %w", err)
	}

	return nil
}
