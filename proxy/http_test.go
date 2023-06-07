package proxy

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestRegisterOrUpdateBackend(t *testing.T) {
	g := NewWithT(t)
	proxy := New(DefaultOptions)

	path := RequestClone{
		Host:    "foo",
		Service: "bar",
		Port:    8080,
		Object: client.ObjectKey{
			Name:      "foo",
			Namespace: "bar",
		},
	}

	err := proxy.RegisterOrUpdate(path)
	g.Expect(err).NotTo(HaveOccurred(), "could not update backend")
	g.Expect(1).To(Equal(len(proxy.dst)))
	g.Expect(path).To(Equal(proxy.dst[0]))

	path = RequestClone{
		Host:    "foo2",
		Service: "bar2",
		Port:    8080,
		Object: client.ObjectKey{
			Name:      "foo",
			Namespace: "bar",
		},
	}

	err = proxy.RegisterOrUpdate(path)
	g.Expect(err).NotTo(HaveOccurred(), "could not update backend")
	g.Expect(1).To(Equal(len(proxy.dst)))

	g.Expect(path).To(Equal(proxy.dst[0]))
}

func TestRemoveBackend(t *testing.T) {
	g := NewWithT(t)
	proxy := New(DefaultOptions)
	err := proxy.Unregister(client.ObjectKey{
		Name: "does-not-exist",
	})
	g.Expect(err).To(Equal(ErrServiceNotRegistered))

	path := RequestClone{
		Host:    "foo",
		Service: "bar",
		Port:    8080,
		Object: client.ObjectKey{
			Name:      "foo",
			Namespace: "bar",
		},
	}
	_ = proxy.RegisterOrUpdate(path)
	err = proxy.Unregister(path.Object)
	g.Expect(err).To(Not(HaveOccurred()))
	g.Expect(0).To(Equal(len(proxy.dst)))
}

func TestServeHTTP(t *testing.T) {
	g := NewWithT(t)

	tests := []struct {
		name           string
		request        func() *http.Request
		expectHTTPCode int
		expectedClones int
		clones         []RequestClone
	}{
		{
			name: "Return service unavailable if no matching backend was found",
			request: func() *http.Request {
				r, _ := http.NewRequest("GET", "does-not-exists", strings.NewReader("body"))
				return r
			},
			expectHTTPCode: http.StatusServiceUnavailable,
			clones: []RequestClone{
				{
					Host: "foo",
					Object: types.NamespacedName{
						Name:      "clone-1",
						Namespace: "foo",
					},
				},
			},
		},
		{
			name: "Request gets duplicated to receiver and responds with 202",
			request: func() *http.Request {
				r, _ := http.NewRequest("GET", "http://foo", strings.NewReader("body"))
				return r
			},
			expectHTTPCode: http.StatusAccepted,
			expectedClones: 1,
			clones: []RequestClone{
				{
					Host:    "foo",
					Service: "bar",
					Port:    8080,
					Object: types.NamespacedName{
						Name:      "clone-1",
						Namespace: "foo",
					},
				},
			},
		},
		{
			name: "Request gets duplicated to multiple receiver and responds with 202",
			request: func() *http.Request {
				r, _ := http.NewRequest("GET", "http://foo", strings.NewReader("body"))
				return r
			},
			expectHTTPCode: http.StatusAccepted,
			expectedClones: 2,
			clones: []RequestClone{
				{
					Host:    "not-matching-host",
					Service: "bar",
					Port:    8080,
					Object: types.NamespacedName{
						Name:      "clone-1",
						Namespace: "foo",
					},
				},
				{
					Host:    "foo",
					Service: "bar",
					Port:    8080,
					Object: types.NamespacedName{
						Name:      "clone-2",
						Namespace: "foo",
					},
				},
				{
					Host:    "foo",
					Service: "other-bar",
					Port:    8080,
					Object: types.NamespacedName{
						Name:      "clone-3",
						Namespace: "foo",
					},
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var mu sync.Mutex
			var cloneRequests []*http.Request

			opts := DefaultOptions
			opts.Client.Transport = &dummyTransport{
				transport: func(r *http.Request) (*http.Response, error) {
					mu.Lock()
					defer mu.Unlock()
					cloneRequests = append(cloneRequests, r)

					return &http.Response{}, nil
				},
			}
			proxy := New(opts)

			for _, receiver := range test.clones {
				err := proxy.RegisterOrUpdate(receiver)
				g.Expect(err).NotTo(HaveOccurred(), "could not update backend")
			}

			w := httptest.NewRecorder()
			proxy.ServeHTTP(w, test.request())
			g.Expect(test.expectHTTPCode).To(Equal(w.Code))
			_ = w.Result()

			g.Expect(test.expectedClones).To(Equal(len(cloneRequests)))

			for _, r := range cloneRequests {
				b, _ := io.ReadAll(r.Body)
				g.Expect("body").To(Equal(string(b)))

				var match bool
				for _, receiver := range test.clones {
					if r.URL.Host == fmt.Sprintf("%s:%d", receiver.Service, receiver.Port) && r.URL.Scheme == "http" {
						match = true
						break
					}
				}

				g.Expect(match).To(Equal(true))
			}
		})
	}
}

type dummyTransport struct {
	transport func(r *http.Request) (*http.Response, error)
}

func (t *dummyTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return t.transport(r)
}
