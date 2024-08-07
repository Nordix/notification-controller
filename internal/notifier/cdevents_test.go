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
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	eventv1 "github.com/fluxcd/pkg/apis/event/v1beta1"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestCDEvents_Post(t *testing.T) {

	testEvent := eventv1.Event{
		InvolvedObject: corev1.ObjectReference{
			Kind:            "HelmRelease",
			Namespace:       "cluster1-ns1",
			Name:            "podinfo",
			UID:             "b6d37d27-a5e2-4423-9407-c4c2331aa2c6",
			APIVersion:      "helm.toolkit.fluxcd.io/v2beta2",
			ResourceVersion: "437798",
		},
		Severity:  "info",
		Timestamp: metav1.Now(),
		Message:   "Helm install succeeded for release ns1/podinfo.v1 with chart podinfo@6.5.1",
		Reason:    "UpgradeSucceeded",
		Metadata: map[string]string{
			"clustername": "cluster1",
			"namespace":   "ns1",
			"revision":    "6.5.1",
		},
		ReportingController: "helm-controller",
		ReportingInstance:   "helm-controller-5855b58cfb-9f8kj",
	}

	testEvent1 := eventv1.Event{
		InvolvedObject: corev1.ObjectReference{
			Kind:            "HelmRelease",
			Namespace:       "cluster1-ns1",
			Name:            "podinfo",
			UID:             "b6d37d27-a5e2-4423-9407-c4c2331aa2c6",
			APIVersion:      "helm.toolkit.fluxcd.io/v2beta2",
			ResourceVersion: "438289",
		},
		Severity:  "error",
		Timestamp: metav1.Now(),
		Message:   "Cluster state of release ns1/podinfo.v2 has drifted from the desired state:\nDeployment/ns1/podinfo removed",
		Reason:    "DriftDetected",
		Metadata: map[string]string{
			"clustername": "cluster1",
			"namespace":   "ns1",
		},
		ReportingController: "helm-controller",
		ReportingInstance:   "helm-controller-5855b58cfb-9f8kj",
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var payload = CDEventsPayload{}
		err = json.Unmarshal(b, &payload)
		require.NoError(t, err)
		// require.Equal(t, "dev.cdevents.environment.modified.0.1.1", payload.Type)
	}))
	defer ts.Close()

	testURL := "http://localhost:9393"

	cdevent, err := NewCDEvents(testURL, "", nil)
	require.NoError(t, err)

	err = cdevent.Post(context.TODO(), testEvent)
	_ = cdevent.Post(context.TODO(), testEvent1)
	require.NoError(t, err)
}
