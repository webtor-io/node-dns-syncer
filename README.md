# node-dns-syncer
Syncs Cloudflare DNS-records with Kubernetes nodes

Retrives k8s node ips and generates DNS A-records for them.

It makes possible to access this nodes directly. It might be usefull if you want to reduce traffic transitions.

## Example
For example we have 3 nodes with external ips:

```
10.0.0.1
10.0.0.2
10.0.0.3
```

and we have domain zone `example.org`.
This script will generate following DNS A-records:

```
abra--0a000001.example.org 10.0.0.1
abra--0a000002.example.org 10.0.0.2
abra--0a000003.example.org 10.0.0.3
```

`0a000001` is just hex representaion of original ip-address `10.0.0.1`

## Usage

```
% ./node-dns-syncer help sync
Syncs Cloudflare DNS-records with Kubernetes nodes

Usage:
  node-dns-syncer sync [flags]

Flags:
      --cf-api-email string         Cloudflare API Email
      --cf-api-key string           Cloudflare API Key
      --cf-zones strings            Cloudflare zones
      --domain-name-prefix string   Domain name prefix
      --domain-name-suffix string   Domain name suffix
      --dry-run                     Dry run
  -h, --help                        help for sync
      --k8s-label-selector string   Kubernetes node label selector

Global Flags:
      --config string   config file (default is $HOME/.node-dns-syncer.yaml)
```

## Helm chart
You can find helm-chart [there](https://github.com/webtor-io/helm-charts/tree/master/charts/node-dns-syncer).
