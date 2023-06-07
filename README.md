# k8sreq-duplicator-controller - Proxy for duplicating HTTP requests to multiple targets

[![release](https://img.shields.io/github/release/DoodleScheduling/k8sreq-duplicator-controller/all.svg)](https://github.com/DoodleScheduling/k8sreq-duplicator-controller/releases)
[![release](https://github.com/doodlescheduling/k8sreq-duplicator-controller/actions/workflows/release.yaml/badge.svg)](https://github.com/doodlescheduling/k8sreq-duplicator-controller/actions/workflows/release.yaml)
[![report](https://goreportcard.com/badge/github.com/DoodleScheduling/k8sreq-duplicator-controller)](https://goreportcard.com/report/github.com/DoodleScheduling/k8sreq-duplicator-controller)
[![Coverage Status](https://coveralls.io/repos/github/DoodleScheduling/k8sreq-duplicator-controller/badge.svg?branch=master)](https://coveralls.io/github/DoodleScheduling/k8sreq-duplicator-controller?branch=master)
[![license](https://img.shields.io/github/license/DoodleScheduling/k8sreq-duplicator-controller.svg)](https://github.com/DoodleScheduling/k8sreq-duplicator-controller/blob/master/LICENSE)

This HTTP proxy duplicates incoming requests and sends them in parallel to multiple targets.
The response will be HTTP 202 Accepted if at least one matching target was found. The responses from the targets are not exposed
to upstream by design.

## Why?
This proxy is especially useful for handling incoming webhooks which need to be distributed to multiple targets.
However it can be used for any other use case where a request needs to be duplicated.

This proxy is designed to be deployed to kubernetes can targets are configured using a `RequestClone` CRD.

## Example RequestClone

These two targets both match `webhook-receiver.example.com`, meaning any incoming request will be duplicated to both endpoints.
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
              client                                            k8sreq      
[webhook-receiver.example.com]  <=                          <=          => webhook-receiver-app-2:80
                                          202 Accepted
```


## Setup

The proxy should not be exposed directly to the public. Rather should traffic be routed via an ingress controller
and only hosts which are used to duplicate requests should be routed via this proxy.

### Helm chart

Please see [chart/k8sreq-duplicator-controller](https://github.com/DoodleScheduling/k8sreq-duplicator-controller) for the helm chart docs.

### Manifests/kustomize

Alternatively you may get the bundled manifests in each release to deploy it using kustomize or use them directly.

## Configure the controller

You may change base settings for the controller using env variables (or alternatively command line arguments).
Available env variables:

| Name  | Description | Default |
|-------|-------------| --------|
| `METRICS_ADDR` | The address of the metric endpoint binds to. | `:9556` |
| `PROBE_ADDR` | The address of the probe endpoints binds to. | `:9557` |
| `HTTP_ADDR` | The address of the http proxy. | `:8080` |
| `PROXY_READ_TIMEOUT` | Read timeout to the proxy backend. | `30s` |
| `PROXY_WRITE_TIMEOUT` | Write timeout to the proxy backend. | `30s` |
| `ENABLE_LEADER_ELECTION` | Enable leader election for controller manager. | `false` |
| `LEADER_ELECTION_NAMESPACE` | Change the leader election namespace. This is by default the same where the controller is deployed. | `` |
| `NAMESPACES` | The controller listens by default for all namespaces. This may be limited to a comma delimted list of dedicated namespaces. | `` |
| `CONCURRENT` | The number of concurrent reconcile workers.  | `2` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | The gRPC opentelemtry-collector endpoint uri | `` |

**Note:** The proxy implements opentelemetry tracing, see [further possible env](https://opentelemetry.io/docs/reference/specification/sdk-environment-variables/) variables to configure it.
