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

package server

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	eventv1 "github.com/fluxcd/pkg/apis/event/v1beta1"
	"github.com/fluxcd/pkg/masktoken"

	apiv1 "github.com/fluxcd/notification-controller/api/v1beta2"
	"github.com/fluxcd/notification-controller/internal/notifier"
)

func (s *EventServer) handleEvent() func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Context()
		body, err := io.ReadAll(r.Body)
		if err != nil {
			s.logger.Error(err, "reading the request body failed")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		defer r.Body.Close()

		event := &eventv1.Event{}
		err = json.Unmarshal(body, event)
		if err != nil {
			s.logger.Error(err, "decoding the request body failed")
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		cleanupMetadata(event)

		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()

		alerts, err := s.getAllAlertsForEvent(ctx, event)
		if err != nil {
			s.logger.Error(err, "failed to get alerts for the event: %w", err)
		}

		if len(alerts) == 0 {
			s.logger.Info("Discarding event, no alerts found for the involved object",
				"reconciler kind", event.InvolvedObject.Kind,
				"name", event.InvolvedObject.Name,
				"namespace", event.InvolvedObject.Namespace)
			w.WriteHeader(http.StatusAccepted)
			return
		}

		s.logger.Info(fmt.Sprintf("Dispatching event: %s", event.Message),
			"reconciler kind", event.InvolvedObject.Kind,
			"name", event.InvolvedObject.Name,
			"namespace", event.InvolvedObject.Namespace)

		// Dispatch notifications
		for _, alert := range alerts {
			if err := s.dispatchNotification(ctx, event, alert); err != nil {
				s.logger.Error(err, "failed to dispatch notification to provider",
					"reconciler kind", apiv1.ProviderKind,
					"name", alert.Spec.ProviderRef.Name,
					"namespace", alert.Namespace)
			}
		}

		w.WriteHeader(http.StatusAccepted)
	}
}

func (s *EventServer) getAllAlertsForEvent(ctx context.Context, event *eventv1.Event) ([]apiv1.Alert, error) {
	var allAlerts apiv1.AlertList
	err := s.kubeClient.List(ctx, &allAlerts)
	if err != nil {
		return nil, fmt.Errorf("failed listing alerts: %w", err)
	}

	return s.filterAlertsForEvent(ctx, allAlerts.Items, event), nil
}

// filterAlertsForEvent filters a given set of alerts against a given event,
// checking if the event matches with any of the alert event sources and is
// allowed by the exclusion list.
func (s *EventServer) filterAlertsForEvent(ctx context.Context, alerts []apiv1.Alert, event *eventv1.Event) []apiv1.Alert {
	results := make([]apiv1.Alert, 0)
	for _, alert := range alerts {
		// Skip suspended alert.
		if alert.Spec.Suspend {
			continue
		}
		// Check if the event matches any of the alert sources.
		if !s.eventMatchesAlertSources(ctx, event, alert) {
			continue
		}
		// Check if the event message is allowed for the alert.
		if s.messageIsExcluded(event.Message, alert.Spec.ExclusionList) {
			continue
		}
		results = append(results, alert)
	}
	return results
}

// eventMatchesAlertSources returns if a given event matches with any of the
// alert sources.
func (s *EventServer) eventMatchesAlertSources(ctx context.Context, event *eventv1.Event, alert apiv1.Alert) bool {
	for _, source := range alert.Spec.EventSources {
		if source.Namespace == "" {
			source.Namespace = alert.Namespace
		}
		if s.eventMatchesAlert(ctx, event, source, alert.Spec.EventSeverity) {
			return true
		}
	}
	return false
}

// messageIsExcluded returns if the given message matches with the exclusion
// rules.
func (s *EventServer) messageIsExcluded(msg string, exclusionList []string) bool {
	if len(exclusionList) == 0 {
		return false
	}

	for _, exp := range exclusionList {
		if r, err := regexp.Compile(exp); err == nil {
			if r.Match([]byte(msg)) {
				return true
			}
		} else {
			// TODO: Record event on the respective Alert object.
			s.logger.Error(err, fmt.Sprintf("failed to compile regex: %s", exp))
		}
	}
	return false
}

// dispatchNotification constructs and sends notification from the given event
// and alert data.
func (s *EventServer) dispatchNotification(ctx context.Context, event *eventv1.Event, alert apiv1.Alert) error {
	sender, notification, token, timeout, err := s.getNotificationParams(ctx, event, alert)
	if err != nil {
		return err
	}
	// Skip when either sender or notification couldn't be created.
	if sender == nil || notification == nil {
		return nil
	}

	go func(n notifier.Interface, e eventv1.Event) {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		if err := n.Post(ctx, e); err != nil {
			maskedErrStr, maskErr := masktoken.MaskTokenFromString(err.Error(), token)
			if maskErr != nil {
				err = maskErr
			} else {
				err = errors.New(maskedErrStr)
			}
			// TODO: Record failed event on the associated Alert object.
			s.logger.Error(err, "failed to send notification",
				"reconciler kind", event.InvolvedObject.Kind,
				"name", event.InvolvedObject.Name,
				"namespace", event.InvolvedObject.Namespace)
		}
	}(sender, *notification)

	return nil
}

// getNotificationParams constructs the notification parameters from the given
// event and alert, and returns a notifier, event, token and timeout for sending
// the notification. The returned event is a mutated form of the input event
// based on the alert configuration.
func (s *EventServer) getNotificationParams(ctx context.Context, event *eventv1.Event, alert apiv1.Alert) (notifier.Interface, *eventv1.Event, string, time.Duration, error) {
	// Check if event comes from a different namespace.
	if s.noCrossNamespaceRefs && event.InvolvedObject.Namespace != alert.Namespace {
		accessDenied := fmt.Errorf(
			"alert '%s/%s' can't process event from '%s/%s/%s', cross-namespace references have been blocked",
			alert.Namespace, alert.Name, event.InvolvedObject.Kind, event.InvolvedObject.Namespace, event.InvolvedObject.Name)
		return nil, nil, "", 0, fmt.Errorf("discarding event, access denied to cross-namespace sources: %w", accessDenied)
	}

	var provider apiv1.Provider
	providerName := types.NamespacedName{Namespace: alert.Namespace, Name: alert.Spec.ProviderRef.Name}

	err := s.kubeClient.Get(ctx, providerName, &provider)
	if err != nil {
		return nil, nil, "", 0, fmt.Errorf("failed to read provider: %w", err)
	}

	// Skip if the provider is suspended.
	if provider.Spec.Suspend {
		return nil, nil, "", 0, nil
	}

	sender, token, err := createNotifier(ctx, s.kubeClient, provider)
	if err != nil {
		return nil, nil, "", 0, fmt.Errorf("failed to initialize notifier: %w", err)
	}

	notification := *event.DeepCopy()
	if alert.Spec.Summary != "" {
		if notification.Metadata == nil {
			notification.Metadata = map[string]string{
				"summary": alert.Spec.Summary,
			}
		} else {
			notification.Metadata["summary"] = alert.Spec.Summary
		}
	}

	return sender, &notification, token, provider.GetTimeout(), nil
}

func createNotifier(ctx context.Context, kubeClient client.Client, provider apiv1.Provider) (notifier.Interface, string, error) {
	webhook := provider.Spec.Address
	username := provider.Spec.Username
	proxy := provider.Spec.Proxy
	token := ""
	password := ""
	headers := make(map[string]string)
	if provider.Spec.SecretRef != nil {
		var secret corev1.Secret
		secretName := types.NamespacedName{Namespace: provider.Namespace, Name: provider.Spec.SecretRef.Name}

		err := kubeClient.Get(ctx, secretName, &secret)
		if err != nil {
			return nil, "", fmt.Errorf("failed to read secret: %w", err)
		}

		if address, ok := secret.Data["address"]; ok {
			webhook = string(address)
			_, err := url.Parse(webhook)
			if err != nil {
				return nil, "", fmt.Errorf("invalid address in secret '%s': %w", webhook, err)
			}
		}

		if p, ok := secret.Data["password"]; ok {
			password = string(p)
		}

		if p, ok := secret.Data["proxy"]; ok {
			proxy = string(p)
			_, err := url.Parse(proxy)
			if err != nil {
				return nil, "", fmt.Errorf("invalid proxy in secret '%s': %w", proxy, err)
			}
		}

		if t, ok := secret.Data["token"]; ok {
			token = string(t)
		}

		if u, ok := secret.Data["username"]; ok {
			username = string(u)
		}

		if h, ok := secret.Data["headers"]; ok {
			err := yaml.Unmarshal(h, &headers)
			if err != nil {
				return nil, "", fmt.Errorf("failed to read headers from secret: %w", err)
			}
		}
	}

	var certPool *x509.CertPool
	if provider.Spec.CertSecretRef != nil {
		var secret corev1.Secret
		secretName := types.NamespacedName{Namespace: provider.Namespace, Name: provider.Spec.CertSecretRef.Name}

		err := kubeClient.Get(ctx, secretName, &secret)
		if err != nil {
			return nil, "", fmt.Errorf("failed to read cert secret: %w", err)
		}

		caFile, ok := secret.Data["caFile"]
		if !ok {
			return nil, "", fmt.Errorf("failed to read secret key caFile: %w", err)
		}

		certPool = x509.NewCertPool()
		ok = certPool.AppendCertsFromPEM(caFile)
		if !ok {
			return nil, "", fmt.Errorf("could not append to cert pool: %w", err)
		}
	}

	if webhook == "" {
		return nil, "", fmt.Errorf("provider has no address")
	}

	factory := notifier.NewFactory(webhook, proxy, username, provider.Spec.Channel, token, headers, certPool, password, string(provider.UID))
	sender, err := factory.Notifier(provider.Spec.Type)
	if err != nil {
		return nil, "", fmt.Errorf("failed to initialize notifier: %w", err)
	}
	return sender, token, nil
}

// eventMatchesAlert returns if a given event matches with the given alert
// source configuration and severity.
func (s *EventServer) eventMatchesAlert(ctx context.Context, event *eventv1.Event, source apiv1.CrossNamespaceObjectReference, severity string) bool {
	// No match if the event and source don't have the same namespace and kind.
	if event.InvolvedObject.Namespace != source.Namespace ||
		event.InvolvedObject.Kind != source.Kind {
		return false
	}

	// No match if the alert severity doesn't match the event severity and
	// the alert severity isn't info.
	if event.Severity != severity && severity != eventv1.EventSeverityInfo {
		return false
	}

	// No match if the source name isn't wildcard, and source and event names
	// don't match.
	if source.Name != "*" && source.Name != event.InvolvedObject.Name {
		return false
	}

	// Match if no match labels specified.
	if source.MatchLabels == nil {
		return true
	}

	// Perform label selector matching.
	var obj metav1.PartialObjectMetadata
	obj.SetGroupVersionKind(event.InvolvedObject.GroupVersionKind())
	obj.SetName(event.InvolvedObject.Name)
	obj.SetNamespace(event.InvolvedObject.Namespace)

	if err := s.kubeClient.Get(ctx, types.NamespacedName{
		Namespace: event.InvolvedObject.Namespace,
		Name:      event.InvolvedObject.Name,
	}, &obj); err != nil {
		s.logger.Error(err, "error getting object", "kind", event.InvolvedObject.Kind,
			"name", event.InvolvedObject.Name, "apiVersion", event.InvolvedObject.APIVersion)
	}

	sel, err := metav1.LabelSelectorAsSelector(&metav1.LabelSelector{
		MatchLabels: source.MatchLabels,
	})
	if err != nil {
		s.logger.Error(err, fmt.Sprintf("error using matchLabels from event source '%s'", source.Name))
	}

	return sel.Matches(labels.Set(obj.GetLabels()))
}

// cleanupMetadata removes metadata entries which are not used for alerting
func cleanupMetadata(event *eventv1.Event) {
	group := event.InvolvedObject.GetObjectKind().GroupVersionKind().Group
	excludeList := []string{fmt.Sprintf("%s/%s", group, eventv1.MetaChecksumKey)}

	meta := make(map[string]string)
	if event.Metadata != nil && len(event.Metadata) > 0 {
		// Filter other meta based on group prefix, while filtering out excludes
		for key, val := range event.Metadata {
			if strings.HasPrefix(key, group) && !inList(excludeList, key) {
				newKey := strings.TrimPrefix(key, fmt.Sprintf("%s/", group))
				meta[newKey] = val
			}
		}
	}

	event.Metadata = meta
}

func inList(l []string, i string) bool {
	for _, v := range l {
		if strings.EqualFold(v, i) {
			return true
		}
	}
	return false
}
