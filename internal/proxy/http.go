package proxy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	ErrServiceNotRegistered = errors.New("service is not registered")
)

const (
	DefaultBodyLimit int64 = 10000000
)

type RequestClone struct {
	Host    string
	Service string
	Port    int32
	Object  client.ObjectKey
}

type HttpProxy struct {
	dst           []RequestClone
	client        *http.Client
	mutex         sync.Mutex
	log           logr.Logger
	bodySizeLimit int64
	wg            sync.WaitGroup
}

type Options struct {
	Logger        logr.Logger
	Client        *http.Client
	BodySizeLimit int64
}

var DefaultOptions = Options{
	Logger:        logr.Discard(),
	Client:        &http.Client{},
	BodySizeLimit: DefaultBodyLimit,
}

func New(opts Options) *HttpProxy {
	return &HttpProxy{
		log:           opts.Logger,
		client:        opts.Client,
		bodySizeLimit: opts.BodySizeLimit,
	}
}

func (h *HttpProxy) Unregister(obj client.ObjectKey) error {
	for k, v := range h.dst {
		if v.Object == obj {
			h.dst = append(h.dst[:k], h.dst[k+1:]...)
			return nil
		}
	}

	return ErrServiceNotRegistered
}

func (h *HttpProxy) RegisterOrUpdate(dst RequestClone) error {
	h.mutex.Lock()
	defer h.mutex.Unlock()

	for k, receiver := range h.dst {
		if receiver.Object == dst.Object {
			h.dst[k].Host = dst.Host
			h.dst[k].Port = dst.Port
			h.dst[k].Service = dst.Service

			return nil
		}
	}

	h.log.Info("register new http backend", "host", dst.Host, "service", dst.Service, "port", dst.Port)
	h.dst = append(h.dst, dst)

	return nil
}

func (h *HttpProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var (
		found bool
		b     []byte
		err   error
	)

	if h.bodySizeLimit == 0 {
		b, err = io.ReadAll(r.Body)
	} else {
		limitedReader := &io.LimitedReader{R: r.Body, N: h.bodySizeLimit}
		b, err = io.ReadAll(limitedReader)
	}

	if err != nil {
		h.log.Error(err, "failed to read incoming body from request", "request", r.RequestURI)
		return
	}

	for _, dst := range h.dst {
		if dst.Host == r.Host {
			found = true
			h.log.Info("found matching http backend for request", "request", r.RequestURI, "host", dst.Host, "service", dst.Service, "port", dst.Port)
			h.wg.Add(1)

			go func(dst RequestClone, r *http.Request, b []byte) {
				defer h.wg.Done()
				clone := r.Clone(context.TODO())

				clone.URL.Scheme = "http"
				clone.URL.Host = fmt.Sprintf("%s:%d", dst.Service, dst.Port)
				clone.RequestURI = ""

				clone.Body = io.NopCloser(bytes.NewReader(b))

				res, err := h.client.Do(clone)
				if err != nil {
					h.log.Error(err, "forwarding request to clone backend failed", "request", r.RequestURI, "host", dst.Host, "service", dst.Service, "port", dst.Port)
				} else {
					h.log.Info("forwarding request to clone backend finished", "status", res.StatusCode, "host", dst.Host, "service", dst.Service, "port", dst.Port)
				}
			}(dst, r, b)
		}
	}

	if found {
		w.WriteHeader(http.StatusAccepted)
	} else {
		// We don't have any matching RequestClone resources matching the host
		w.WriteHeader(http.StatusServiceUnavailable)
	}
}

func (h *HttpProxy) Close() {
	h.wg.Wait()
}
