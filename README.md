# webhook-controller - Proxy for duplicating incoming webhooks to multiple targets

[![release](https://img.shields.io/github/release/DoodleScheduling/webhook-controller/all.svg)](https://github.com/DoodleScheduling/webhook-controller/releases)
[![release](https://github.com/doodlescheduling/webhook-controller/actions/workflows/release.yaml/badge.svg)](https://github.com/doodlescheduling/webhook-controller/actions/workflows/release.yaml)
[![report](https://goreportcard.com/badge/github.com/DoodleScheduling/webhook-controller)](https://goreportcard.com/report/github.com/DoodleScheduling/webhook-controller)
[![OpenSSF Scorecard](https://api.securityscorecards.dev/projects/github.com/DoodleScheduling/webhook-controller/badge)](https://api.securityscorecards.dev/projects/github.com/DoodleScheduling/webhook-controller)
[![Coverage Status](https://coveralls.io/repos/github/DoodleScheduling/webhook-controller/badge.svg?branch=master)](https://coveralls.io/github/DoodleScheduling/webhook-controller?branch=master)
[![license](https://img.shields.io/github/license/DoodleScheduling/webhook-controller.svg)](https://github.com/DoodleScheduling/webhook-controller/blob/master/LICENSE)

This HTTP proxy duplicates incoming requests and sends concurrently to multiple targets.
The response will be HTTP 202 Accepted if at least one matching target was found. The responses from the targets are not exposed
to upstream by design.

## Why?
This proxy is especially useful for handling incoming webhooks which need to be distributed to multiple targets.
However it can be used for any other use case where a request needs to be duplicated.

## Example RequestClone

These two targets both match `webhook-receiver.example.com`, meaning any incoming request will be sent to both endpoints.
In this case to `webhook-receiver-app-1:80` and `webhook-receiver-app-2:80`.

```yaml
apiVersion: proxy.infra.doodle.com/v1beta1
kind: RequestClone
metadata:
  name: webhook-receiver
  namespace: apps
spec:
  host: webhook-receiver.example.com
  backend:
    serviceName: webhook-receiver-app-1
    servicePort: http
---
apiVersion: proxy.infra.doodle.com/v1beta1
kind: RequestClone
metadata:
  name: webhook-receiver
  namespace: apps
spec:
  host: webhook-receiver.example.com
  backend:
    serviceName: webhook-receiver-app-2
    servicePort: http

```
North south routing looks like this:
```
                                                                      
                                => Ingress controller proxy =>          => webhook-receiver-app-1:80
              client                                            webhook      
[webhook-receiver.example.com]  <=                          <=          => webhook-receiver-app-2:80
                                          202 Accepted
```


## Setup

The proxy should not be exposed directly to the public. Rather should traffic be routed via an ingress controller
and only hosts which are used to duplicate requests should be routed via this proxy.

### Helm chart

Please see [chart/webhook-controller](https://github.com/DoodleScheduling/webhook-controller) for the helm chart docs.

### Manifests/kustomize

Alternatively you may get the bundled manifests in each release to deploy it using kustomize or use them directly.

## Configure the controller

The controller can be configured using cmd args:
```
--concurrent int                            The number of concurrent Pod reconciles. (default 4)
--enable-leader-election                    Enable leader election for controller manager. Enabling this will ensure there is only one active controller manager.
--graceful-shutdown-timeout duration        The duration given to the reconciler to finish before forcibly stopping. (default 10m0s)
--health-addr string                        The address the health endpoint binds to. (default ":9557")
--http-addr string                          The address of http server binding to. (default ":8080")
--insecure-kubeconfig-exec                  Allow use of the user.exec section in kubeconfigs provided for remote apply.
--insecure-kubeconfig-tls                   Allow that kubeconfigs provided for remote apply can disable TLS verification.
--kube-api-burst int                        The maximum burst queries-per-second of requests sent to the Kubernetes API. (default 300)
--kube-api-qps float32                      The maximum queries-per-second of requests sent to the Kubernetes API. (default 50)
--leader-election-lease-duration duration   Interval at which non-leader candidates will wait to force acquire leadership (duration string). (default 35s)
--leader-election-release-on-cancel         Defines if the leader should step down voluntarily on controller manager shutdown. (default true)
--leader-election-renew-deadline duration   Duration that the leading controller manager will retry refreshing leadership before giving up (duration string). (default 30s)
--leader-election-retry-period duration     Duration the LeaderElector clients should wait between tries of actions (duration string). (default 5s)
--log-encoding string                       Log encoding format. Can be 'json' or 'console'. (default "json")
--log-level string                          Log verbosity level. Can be one of 'trace', 'debug', 'info', 'error'. (default "info")
--max-retry-delay duration                  The maximum amount of time for which an object being reconciled will have to wait before a retry. (default 15m0s)
--metrics-addr string                       The address the metric endpoint binds to. (default ":9556")
--min-retry-delay duration                  The minimum amount of time for which an object being reconciled will have to wait before a retry. (default 750ms)
--otel-endpoint string                      Opentelemetry gRPC endpoint (without protocol)
--otel-insecure                             Opentelemetry gRPC disable tls
--otel-service-name string                  Opentelemetry service name (default "k8skeycloak-controller")
--otel-tls-client-cert-path string          Opentelemetry gRPC mTLS client cert path
--otel-tls-client-key-path string           Opentelemetry gRPC mTLS client key path
--otel-tls-root-ca-path string              Opentelemetry gRPC mTLS root CA path
--proxy-read-timeout duration               Read timeout for proxy requests. (default 10s)
--proxy-write-timeout duration              Write timeout for proxy requests. (default 10s)
--watch-all-namespaces                      Watch for resources in all namespaces, if set to false it will only watch the runtime namespace. (default true)
--watch-label-selector string               Watch for resources with matching labels e.g. 'sharding.fluxcd.io/shard=shard1'.
```
