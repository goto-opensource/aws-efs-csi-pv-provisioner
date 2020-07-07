# AWS EFS CSI PV provisioner

Kubernetes CSI driver to dynamically provisions Persistent Volumes (PVs) in response to user-requested Persistent Volume Clains (PVCs). Each PV / PVC is a subdirectory on a single, cluster-wide EFS file system. Works in conjunction with the [AWS EFS CSI driver](https://github.com/kubernetes-sigs/aws-efs-csi-driver).

## Installation

1. Create an EFS filesystem and mount targets for your cluster.
2. Install the [AWS EFS CSI driver](https://github.com/kubernetes-sigs/aws-efs-csi-driver) with a corresponding `StorageClass` called `efs-sc`.
3. Build a Docker image with the Dockerfile in this repository and push it into a repository. Put the image URL into `deploy/deployment.yaml`.
4. Put your EFS file system IDs into `deploy/deployment.yaml` and `deploy/pv.yaml`.
5. (Optional) Modify the desired mount options in `deploy/pv.yaml` and `deploy/sc.yaml` (e.g. to disable TLS and IAM if not needed).
6. Apply the manifests: `kubectl -n kube-system apply -f deploy/`

### Creating a PVC

Apply the following manifest:

```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: my-efs-pvc
  namespace: default
spec:
  accessModes:
  - ReadWriteMany
  resources:
    requests:
      storage: 1Mi # doesn't matter but is a required field
  storageClassName: efs
  volumeMode: Filesystem
```

A corresponding PV called `pvc-<UID of PVC>` will be created bound to the PVC. This PV is utilizing the AWS EFS CSI driver. A subdirectory called `<namespace of PVC>-<name of PVC>-<name of PV>` will be created on the configured EFS file system:

```yaml
apiVersion: v1
kind: PersistentVolume
metadata:
  name: pvc-cdd36709-bd3b-11ea-9990-12db9e7ffa3d
spec:
  accessModes:
  - ReadWriteMany
  capacity:
    storage: 1Mi
  claimRef:
    apiVersion: v1
    kind: PersistentVolumeClaim
    name: my-efs-pvc
    namespace: default
    resourceVersion: "836626609"
    uid: cdd36709-bd3b-11ea-9990-12db9e7ffa3d
  csi:
    driver: efs.csi.aws.com
    volumeHandle: fs-12345678:/persistentvolumes/default-my-efs-pvc-pvc-cdd36709-bd3b-11ea-9990-12db9e7ffa3d
  mountOptions:
  - tls
  - iam
  persistentVolumeReclaimPolicy: Delete
  storageClassName: efs
  volumeMode: Filesystem
status:
  phase: Bound
```

When a pod is requesting to have this PVC mounted, the AWS EFS CSI driver daemonset will take care of executing the actual mount. The AWS EFS CSI PV provisioner in this repository is only responsible for creating the PVs in response to PVCs.

## "How it works" overview diagram

![](docs/overview.svg)

## TODOs

* create CI to build and push Docker image
* provide Helm chart
* potentially integerate into AWS EFS CSI driver
