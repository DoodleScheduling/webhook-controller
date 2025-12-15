package proxy

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr"
	. "github.com/onsi/gomega"
)

func TestRegisterOrUpdateBackend(t *testing.T) {
	g := NewWithT(t)
	proxy := New(DefaultOptions)

	receiver := Receiver{
		Path:         "/test",
		ResponseType: Async,
		Targets: []Target{
			{
				Address:     "foo",
				Port:        8080,
				ServiceName: "bar",
			},
		},
	}

	err := proxy.RegisterOrUpdate(receiver)
	g.Expect(err).NotTo(HaveOccurred(), "could not update backend")
	g.Expect(1).To(Equal(len(proxy.receivers)))
	g.Expect(receiver).To(Equal(proxy.receivers["/test"]))

	receiver = Receiver{
		Path:         "/test",
		ResponseType: Async,
		Targets: []Target{
			{
				Address:     "foo2",
				Port:        8080,
				ServiceName: "bar2",
			},
		},
	}

	err = proxy.RegisterOrUpdate(receiver)
	g.Expect(err).NotTo(HaveOccurred(), "could not update backend")
	g.Expect(1).To(Equal(len(proxy.receivers)))

	g.Expect(receiver).To(Equal(proxy.receivers["/test"]))
}

func TestRemoveBackend(t *testing.T) {
	g := NewWithT(t)
	proxy := New(DefaultOptions)

	receiver := Receiver{
		Path:         "/test",
		ResponseType: Async,
		Targets: []Target{
			{
				Address:     "foo",
				Port:        8080,
				ServiceName: "bar",
			},
		},
	}
	_ = proxy.RegisterOrUpdate(receiver)
	err := proxy.Unregister("/test")
	g.Expect(err).To(Not(HaveOccurred()))
	g.Expect(0).To(Equal(len(proxy.receivers)))
}

func TestServeHTTP_Async(t *testing.T) {
	g := NewWithT(t)

	tests := []struct {
		name           string
		request        func() *http.Request
		expectHTTPCode int
		expectedClones int
		receiver       Receiver
	}{
		{
			name: "Return service unavailable if no matching backend was found",
			request: func() *http.Request {
				r, _ := http.NewRequest("GET", "http://example.com/does-not-exist", strings.NewReader("body"))
				return r
			},
			expectHTTPCode: http.StatusServiceUnavailable,
			receiver: Receiver{
				Path:         "/test",
				ResponseType: Async,
				Targets: []Target{
					{
						Address:     "foo",
						Port:        8080,
						ServiceName: "bar",
					},
				},
			},
		},
		{
			name: "Request gets duplicated to receiver and responds with 202",
			request: func() *http.Request {
				r, _ := http.NewRequest("GET", "http://example.com/test", strings.NewReader("body"))
				return r
			},
			expectHTTPCode: http.StatusAccepted,
			expectedClones: 1,
			receiver: Receiver{
				Path:         "/test",
				ResponseType: Async,
				Targets: []Target{
					{
						Address:     "foo",
						Port:        8080,
						ServiceName: "bar",
					},
				},
			},
		},
		{
			name: "Request gets duplicated to multiple targets and responds with 202",
			request: func() *http.Request {
				r, _ := http.NewRequest("GET", "http://example.com/test", strings.NewReader("body"))
				return r
			},
			expectHTTPCode: http.StatusAccepted,
			expectedClones: 2,
			receiver: Receiver{
				Path:         "/test",
				ResponseType: Async,
				Targets: []Target{
					{
						Address:     "foo",
						Port:        8080,
						ServiceName: "bar",
					},
					{
						Address:     "foo2",
						Port:        8080,
						ServiceName: "bar2",
					},
				},
			},
		},
		{
			name: "No targets returns internal server error",
			request: func() *http.Request {
				r, _ := http.NewRequest("GET", "http://example.com/test", strings.NewReader("body"))
				return r
			},
			expectHTTPCode: http.StatusInternalServerError,
			expectedClones: 0,
			receiver: Receiver{
				Path:         "/test",
				ResponseType: Async,
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

			err := proxy.RegisterOrUpdate(test.receiver)
			g.Expect(err).NotTo(HaveOccurred(), "could not update backend")

			w := httptest.NewRecorder()
			proxy.ServeHTTP(w, test.request())
			proxy.Close()

			g.Expect(test.expectHTTPCode).To(Equal(w.Code))
			_ = w.Result()

			g.Expect(test.expectedClones).To(Equal(len(cloneRequests)))

			for _, r := range cloneRequests {
				b, _ := io.ReadAll(r.Body)
				g.Expect("body").To(Equal(string(b)))

				var match bool
				for _, target := range test.receiver.Targets {
					if r.URL.Host == fmt.Sprintf("%s:%d", target.Address, target.Port) && r.URL.Scheme == "http" {
						match = true
						break
					}
				}

				g.Expect(match).To(Equal(true))
			}
		})
	}
}

func TestServeHTTP_Sync(t *testing.T) {
	g := NewWithT(t)

	tests := []struct {
		name            string
		responses       []*http.Response
		expectedCode    int
		timeout         time.Duration
		expectedBody    string
		expectedHeaders map[string]string
		responseType    ResponseType
	}{
		{
			name:         "Returns first successful response",
			responseType: AwaitAllPreferSuccessful,
			responses: []*http.Response{
				{StatusCode: 500, Body: io.NopCloser(strings.NewReader("error"))},
				{StatusCode: 200, Body: io.NopCloser(strings.NewReader("success")), Header: http.Header{"X-Test": []string{"value"}}},
				{StatusCode: 201, Body: io.NopCloser(strings.NewReader("created"))},
			},
			expectedCode:    200,
			expectedBody:    "success",
			expectedHeaders: map[string]string{"X-Test": "value"},
		},
		{
			name:         "Returns first failed response",
			responseType: AwaitAllPreferFailed,
			responses: []*http.Response{
				{StatusCode: 500, Body: io.NopCloser(strings.NewReader("error"))},
				{StatusCode: 200, Body: io.NopCloser(strings.NewReader("success")), Header: http.Header{"X-Test": []string{"value"}}},
				{StatusCode: 201, Body: io.NopCloser(strings.NewReader("created"))},
			},
			expectedCode: 500,
			expectedBody: "error",
		},
		{
			name:         "Returns first successful response even if later ones fail",
			responseType: AwaitAllPreferSuccessful,
			responses: []*http.Response{
				{StatusCode: 200, Body: io.NopCloser(strings.NewReader("first"))},
				{StatusCode: 500, Body: io.NopCloser(strings.NewReader("error"))},
			},
			expectedCode: 200,
			expectedBody: "first",
		},
		{
			name:         "Returns last failed response as there is no successful one",
			responseType: AwaitAllPreferSuccessful,
			responses: []*http.Response{
				{StatusCode: 501, Body: io.NopCloser(strings.NewReader("first"))},
				{StatusCode: 500, Body: io.NopCloser(strings.NewReader("second")), Header: http.Header{"X-Test": []string{"value"}}},
			},
			expectedCode:    500,
			expectedBody:    "second",
			expectedHeaders: map[string]string{"X-Test": "value"},
		},
		{
			name:         "Returns last sucessful response as there is no failed one",
			responseType: AwaitAllPreferFailed,
			responses: []*http.Response{
				{StatusCode: 201, Body: io.NopCloser(strings.NewReader("first"))},
				{StatusCode: 200, Body: io.NopCloser(strings.NewReader("second")), Header: http.Header{"X-Test": []string{"value"}}},
			},
			expectedCode:    200,
			expectedBody:    "second",
			expectedHeaders: map[string]string{"X-Test": "value"},
		},
		{
			name:         "Returns first error response as there is no successful one",
			responseType: AwaitAllPreferSuccessful,
			timeout:      20 * time.Millisecond,
			responses: []*http.Response{
				{StatusCode: 0},
				{StatusCode: 500, Body: io.NopCloser(strings.NewReader("second"))},
			},
			expectedCode: 504,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var mu sync.Mutex
			responseIndex := 0

			opts := DefaultOptions
			opts.Client.Transport = &dummyTransport{
				transport: func(r *http.Request) (*http.Response, error) {
					mu.Lock()
					defer mu.Unlock()

					idx := responseIndex
					responseIndex++

					if idx < len(test.responses) {
						return test.responses[idx], nil
					}
					return &http.Response{StatusCode: 500}, nil
				},
			}
			proxy := New(opts)

			receiver := Receiver{
				Path:         "/test",
				Timeout:      test.timeout,
				ResponseType: test.responseType,
				Targets:      make([]Target, len(test.responses)),
			}
			for i := range test.responses {
				receiver.Targets[i] = Target{
					Address:     fmt.Sprintf("target%d", i),
					Port:        8080,
					ServiceName: "service",
				}
			}

			err := proxy.RegisterOrUpdate(receiver)
			g.Expect(err).NotTo(HaveOccurred())

			req, _ := http.NewRequest("GET", "http://example.com/test", strings.NewReader("body"))
			w := httptest.NewRecorder()
			proxy.ServeHTTP(w, req)
			proxy.Close()

			g.Expect(test.expectedCode).To(Equal(w.Code))
			g.Expect(test.expectedBody).To(Equal(w.Body.String()))
			for k, v := range test.expectedHeaders {
				g.Expect(v).To(Equal(w.Header().Get(k)))
			}
		})
	}
}

func TestServeHTTP_BodySizeLimit(t *testing.T) {
	g := NewWithT(t)

	tests := []struct {
		name            string
		bodySizeLimit   int64
		requestBody     string
		expectedReadLen int
	}{
		{
			name:            "No limit reads full body",
			bodySizeLimit:   0,
			requestBody:     "this is a test body",
			expectedReadLen: len("this is a test body"),
		},
		{
			name:            "With limit reads only up to limit",
			bodySizeLimit:   5,
			requestBody:     "this is a test body",
			expectedReadLen: 5,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var mu sync.Mutex
			var receivedBody []byte

			opts := DefaultOptions
			opts.Client.Transport = &dummyTransport{
				transport: func(r *http.Request) (*http.Response, error) {
					mu.Lock()
					defer mu.Unlock()
					receivedBody, _ = io.ReadAll(r.Body)
					return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("ok"))}, nil
				},
			}
			proxy := New(opts)

			receiver := Receiver{
				Path:          "/test",
				ResponseType:  Async,
				BodySizeLimit: test.bodySizeLimit,
				Targets: []Target{
					{
						Address:     "target",
						Port:        8080,
						ServiceName: "service",
					},
				},
			}

			err := proxy.RegisterOrUpdate(receiver)
			g.Expect(err).NotTo(HaveOccurred())

			req, _ := http.NewRequest("POST", "http://example.com/test", strings.NewReader(test.requestBody))
			w := httptest.NewRecorder()
			proxy.ServeHTTP(w, req)
			proxy.Close()

			g.Expect(test.expectedReadLen).To(Equal(len(receivedBody)))
		})
	}
}

func TestServeHTTP_Timeout(t *testing.T) {
	g := NewWithT(t)

	opts := DefaultOptions
	opts.Client.Transport = &dummyTransport{
		transport: func(r *http.Request) (*http.Response, error) {
			// Check if context has timeout
			select {
			case <-r.Context().Done():
				return nil, r.Context().Err()
			default:
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("ok"))}, nil
			}
		},
	}
	proxy := New(opts)

	receiver := Receiver{
		Path:         "/test",
		ResponseType: AwaitAllPreferSuccessful,
		Timeout:      100 * time.Millisecond,
		Targets: []Target{
			{
				Address:     "target",
				Port:        8080,
				ServiceName: "service",
			},
		},
	}

	err := proxy.RegisterOrUpdate(receiver)
	g.Expect(err).NotTo(HaveOccurred())

	req, _ := http.NewRequest("GET", "http://example.com/test", strings.NewReader("body"))
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)
	proxy.Close()

	// Request should complete successfully with timeout context
	g.Expect(http.StatusOK).To(Equal(w.Code))
}

func TestNew(t *testing.T) {
	g := NewWithT(t)

	opts := Options{
		Logger: logr.Discard(),
		Client: &http.Client{Timeout: 5 * time.Second},
	}

	proxy := New(opts)

	g.Expect(proxy).NotTo(BeNil())
	g.Expect(proxy.receivers).NotTo(BeNil())
	g.Expect(proxy.client).To(Equal(opts.Client))
	g.Expect(len(proxy.receivers)).To(Equal(0))
}

type dummyTransport struct {
	transport func(r *http.Request) (*http.Response, error)
}

func (t *dummyTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return t.transport(r)
}
