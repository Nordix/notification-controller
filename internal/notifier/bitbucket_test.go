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
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	fuzz "github.com/AdaLogics/go-fuzz-headers"
	"github.com/fluxcd/pkg/runtime/events"
	"github.com/stretchr/testify/assert"
)

func TestNewBitbucketBasic(t *testing.T) {
	b, err := NewBitbucket("0c9c2e41-d2f9-4f9b-9c41-bebc1984d67a", "https://bitbucket.org/foo/bar", "foo:bar", nil)
	assert.Nil(t, err)
	assert.Equal(t, b.Owner, "foo")
	assert.Equal(t, b.Repo, "bar")
}

func TestNewBitbucketInvalidUrl(t *testing.T) {
	_, err := NewBitbucket("0c9c2e41-d2f9-4f9b-9c41-bebc1984d67a", "https://bitbucket.org/foo/bar/baz", "foo:bar", nil)
	assert.NotNil(t, err)
}

func TestNewBitbucketInvalidToken(t *testing.T) {
	_, err := NewBitbucket("0c9c2e41-d2f9-4f9b-9c41-bebc1984d67a", "https://bitbucket.org/foo/bar", "bar", nil)
	assert.NotNil(t, err)
}

func Fuzz_Bitbucket(f *testing.F) {
	f.Add("user:pass", "org/repo", "revision/dsa123a", "info", []byte{}, []byte(`{"state":"SUCCESSFUL","description":"","key":"","name":"","url":""}`))
	f.Add("user:pass", "org/repo", "revision/dsa123a", "error", []byte{}, []byte(`{}`))

	f.Fuzz(func(t *testing.T,
		token, urlSuffix, revision, severity string, seed, response []byte) {

		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.Copy(io.Discard, r.Body)
			w.Write(response)
			r.Body.Close()
		}))
		defer ts.Close()

		var cert x509.CertPool
		_ = fuzz.NewConsumer(seed).GenerateStruct(&cert)

		bitbucket, err := NewBitbucket("0c9c2e41-d2f9-4f9b-9c41-bebc1984d67a", fmt.Sprintf("%s/%s", ts.URL, urlSuffix), token, &cert)
		if err != nil {
			return
		}

		apiUrl, err := url.Parse(ts.URL)
		if err != nil {
			t.Fatalf("cannot parse api base URL: %v", err)
		}
		// Ensure the call does not go to bitbucket and fuzzes the response.
		bitbucket.Client.SetApiBaseURL(*apiUrl)

		event := events.Event{}

		// Try to fuzz the event object, but if it fails (not enough seed),
		// ignore it, as other inputs are also being used in this test.
		_ = fuzz.NewConsumer(seed).GenerateStruct(&event)

		if event.Metadata == nil && (revision != "") {
			event.Metadata = map[string]string{
				"revision": revision,
			}
		}
		event.Severity = severity

		_ = bitbucket.Post(context.TODO(), event)
	})
}
