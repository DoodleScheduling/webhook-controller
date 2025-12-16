package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/go-logr/logr"
)

var (
	ErrServiceNotRegistered = errors.New("service is not registered")
)

type ResponseType string

const (
	Async                    ResponseType = "Async"
	AwaitAllPreferSuccessful ResponseType = "AwaitAllPreferSuccessful"
	AwaitAllPreferFailed     ResponseType = "AwaitAllPreferFailed"
	AwaitAllReport           ResponseType = "AwaitAllReport"
)

type Target struct {
	Path             string
	Address          string
	Port             int32
	ServiceName      string
	ServiceNamespace string
	ResponseType     ResponseType
	BodySizeLimit    int64
}

type Receiver struct {
	Path          string
	Timeout       time.Duration
	Targets       []Target
	ResponseType  ResponseType
	BodySizeLimit int64
}

type HttpProxy struct {
	receivers map[string]Receiver
	client    *http.Client
	mutex     sync.Mutex
	log       logr.Logger
	wg        sync.WaitGroup
}

type Options struct {
	Logger logr.Logger
	Client *http.Client
}

var DefaultOptions = Options{
	Logger: logr.Discard(),
	Client: &http.Client{},
}

func New(opts Options) *HttpProxy {
	return &HttpProxy{
		log:       opts.Logger,
		client:    opts.Client,
		receivers: make(map[string]Receiver),
	}
}

func (h *HttpProxy) Unregister(path string) error {
	h.mutex.Lock()
	defer h.mutex.Unlock()

	delete(h.receivers, path)
	return nil
}

func (h *HttpProxy) RegisterOrUpdate(receiver Receiver) error {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	h.receivers[receiver.Path] = receiver
	return nil
}

type ReportResponse struct {
	Targets []ReportTargetResponse `json:"targets"`
}

type ReportTargetResponse struct {
	StatusCode int                 `json:"statusCode"`
	Body       string              `json:"body,omitempty"`
	Headers    map[string][]string `json:"headers,omitempty"`
}

func (h *HttpProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	receiver, ok := h.receivers[r.URL.Path]
	if !ok {
		h.log.Info("no matching http backend for request", "request", r.RequestURI)
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}

	var (
		b   []byte
		err error
	)

	if receiver.BodySizeLimit == 0 {
		b, err = io.ReadAll(r.Body)
	} else {
		limitedReader := &io.LimitedReader{R: r.Body, N: receiver.BodySizeLimit}
		b, err = io.ReadAll(limitedReader)
	}

	if err != nil {
		h.log.Error(err, "failed to read incoming body from request", "request", r.RequestURI)
		return
	}

	responses := make(chan *http.Response)

	ctx := context.TODO()
	var cancel context.CancelFunc

	if receiver.Timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, receiver.Timeout)
		defer cancel()
	}

	h.log.Info("clone request to upstreams", "targets", len(receiver.Targets), "request", r.RequestURI)

	if len(receiver.Targets) == 0 {
		h.log.Info("no targets found", "request", r.RequestURI)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	for _, dst := range receiver.Targets {
		h.wg.Add(1)

		go func(dst Target, r *http.Request, b []byte) {
			defer h.wg.Done()

			clone := r.Clone(ctx)

			clone.URL.Scheme = "http"
			clone.URL.Host = fmt.Sprintf("%s:%d", dst.Address, dst.Port)
			clone.URL.Path = dst.Path
			clone.RequestURI = ""

			clone.Body = io.NopCloser(bytes.NewReader(b))

			res, err := h.client.Do(clone)
			if err != nil {
				res = &http.Response{
					StatusCode: http.StatusGatewayTimeout,
				}
				h.log.Error(err, "forwarding request to clone backend failed", "request", r.RequestURI, "target", clone.URL.Host, "service", dst.ServiceName, "namespace", dst.ServiceNamespace)
			} else {
				h.log.Info("forwarding request to clone backend finished", "status", res.StatusCode, "target", clone.URL.Host, "service", dst.ServiceName, "namespace", dst.ServiceNamespace)
			}

			if receiver.ResponseType != Async {
				responses <- res
			}
		}(dst, r, b)
	}

	if receiver.ResponseType == Async {
		h.log.Info("return response", "request", r.RequestURI, "status", http.StatusAccepted)
		w.WriteHeader(http.StatusAccepted)
		return
	}

	var lastResponse *http.Response
	var returnResponse *http.Response
	var received int
	var reportResponse ReportResponse

	for response := range responses {
		received++

		if receiver.ResponseType == AwaitAllReport {
			body, err := io.ReadAll(response.Body)
			if err != nil {
				h.log.Error(err, "failed to read response body", "request", r.RequestURI)
				continue
			}

			reportResponse.Targets = append(reportResponse.Targets, ReportTargetResponse{
				StatusCode: response.StatusCode,
				Body:       string(body),
				Headers:    response.Header,
			})
		} else if returnResponse == nil {
			if receiver.ResponseType == AwaitAllPreferSuccessful {
				if response.StatusCode >= 200 && response.StatusCode < 300 {
					returnResponse = response
				}
			} else if receiver.ResponseType == AwaitAllPreferFailed {
				if response.StatusCode >= 400 {
					returnResponse = response
				}
			}
		}

		if received == len(receiver.Targets) {
			lastResponse = response
			close(responses)
			break
		}
	}

	if receiver.ResponseType == AwaitAllReport {
		body, err := json.Marshal(reportResponse)
		if err != nil {
			h.log.Error(err, "failed to marshal report response", "request", r.RequestURI)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(body)
		return
	}

	if returnResponse == nil {
		returnResponse = lastResponse
	}

	h.log.Info("return response", "request", r.RequestURI, "status", returnResponse.StatusCode)

	for k, v := range returnResponse.Header {
		for _, vv := range v {
			w.Header().Add(k, vv)
		}
	}

	w.WriteHeader(returnResponse.StatusCode)

	if returnResponse.Body != nil {
		io.Copy(w, returnResponse.Body)
		defer func() {
			_ = returnResponse.Body.Close()
		}()
	}
}

func (h *HttpProxy) Close() {
	h.wg.Wait()
}
