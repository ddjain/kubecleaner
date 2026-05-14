# kubecleaner

A custom Kubernetes operator built with Kubebuilder that periodically cleans up
cluster resources (Pods, Deployments, Services, Ingresses, PersistentVolumes,
Namespaces) on a configurable interval.

## Features

- **Configurable cleanup interval** via CRD spec (e.g., `5m`, `1h`)
- **Dry-run mode** for safe testing — logs what would be deleted without deleting
- **Label-based exclude filtering** — protect resources with specific labels
- **Optional label selector targeting** — only clean matching resources
- **Hardcoded system namespace protection** — `kube-system`, `kube-public`, `kube-node-lease`, `default` are never touched

## Getting Started

### Prerequisites
- Go version v1.24.6+
- Docker version 17.03+
- kubectl version v1.11.3+
- Access to a Kubernetes v1.11.3+ cluster

### To Deploy on the cluster

**Build and push your image to the location specified by `IMG`:**

```sh
make docker-build docker-push IMG=<some-registry>/kubecleaner:tag
```

**Install the CRDs into the cluster:**

```sh
make install
```

**Deploy the Manager to the cluster with the image specified by `IMG`:**

```sh
make deploy IMG=<some-registry>/kubecleaner:tag
```

**Create instances of your solution:**

```sh
kubectl apply -k config/samples/
```

### Example KubeCleaner CR

```yaml
apiVersion: cleanup.kubecleaner.io/v1alpha1
kind: KubeCleaner
metadata:
  name: kubecleaner-sample
spec:
  interval: "5m"
  dryRun: true
  excludeLabels:
    app.kubernetes.io/managed-by: Helm
    protected: "true"
  selector:
    matchLabels:
      environment: staging
```

### To Uninstall

**Delete the instances (CRs) from the cluster:**

```sh
kubectl delete -k config/samples/
```

**Delete the APIs (CRDs) from the cluster:**

```sh
make uninstall
```

**UnDeploy the controller from the cluster:**

```sh
make undeploy
```

## Testing with kind

```sh
kind create cluster --name kubecleaner-test
make docker-build IMG=kubecleaner:test
kind load docker-image kubecleaner:test --name kubecleaner-test
make install
make deploy IMG=kubecleaner:test
kubectl apply -f config/samples/cleanup_v1alpha1_kubecleaner.yaml
kubectl logs -f deployment/kubecleaner-controller-manager -n kubecleaner-system
```

## License

Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
