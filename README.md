Tirsias
=======

_Automatically create Grafana datasource with kubeproxy+bearer token for each Prometheus deployed in a Kubernetes cluster_

## Usage

```
Usage:
  tirsias [OPTIONS]

Application Options:
  -V, --version                    Display version.
      --kubeconfig=                Path to your kubeconfig file. [$KUBECONFIG]
      --cluster-name=              Name of the Kubernetes cluster. [$CLUSTER_NAME]
      --service-account-name=      Service account name Grafana should use. [$SERVICE_ACCOUNT_NAME]
      --service-account-namespace= Service account namespace Grafana should use. [$SERVICE_ACCOUNT_NAMESPACE]

Grafana instance options:
      --grafana-url=               Address of your Grafana instance. [$GRAFANA_URL]
      --grafana-token=             Authentication token for the Grafana instance. [$GRAFANA_TOKEN]

Help Options:
  -h, --help                       Show this help message
```
