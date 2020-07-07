package main

import (
	"flag"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog"
	"sigs.k8s.io/sig-storage-lib-external-provisioner/controller"
	"sigs.k8s.io/sig-storage-lib-external-provisioner/gidallocator"
)

const provisionerName = "aws.k8s.logmein.com/efs-csi-pv-provisioner"
const efsCsiDriverName = "efs.csi.aws.com"

type efsProvisioner struct {
	fileSystemID string
	mountPoint   string
	subPath      string
	allocator    gidallocator.Allocator
}

var _ controller.Provisioner = &efsProvisioner{}

// Provision creates a storage asset and returns a PV object representing it.
func (p *efsProvisioner) Provision(options controller.ProvisionOptions) (*v1.PersistentVolume, error) {
	if options.PVC.Spec.Selector != nil {
		return nil, fmt.Errorf("claim.Spec.Selector is not supported")
	}

	gidAllocate := true
	for k, v := range options.StorageClass.Parameters {
		switch strings.ToLower(k) {
		case "gidmin":
		// Let allocator handle
		case "gidmax":
		// Let allocator handle
		case "gidallocate":
			b, err := strconv.ParseBool(v)
			if err != nil {
				return nil, fmt.Errorf("invalid value %s for parameter %s: %v", v, k, err)
			}
			gidAllocate = b
		}
	}

	var gid *int
	if gidAllocate {
		allocate, err := p.allocator.AllocateNext(options)
		if err != nil {
			return nil, err
		}
		gid = &allocate
	}

	err := p.createVolume(p.getLocalPath(options), gid)
	if err != nil {
		return nil, err
	}

	mountOptions := []string{}
	if options.StorageClass.MountOptions != nil {
		mountOptions = options.StorageClass.MountOptions
	}

	pv := &v1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: options.PVName,
		},
		Spec: v1.PersistentVolumeSpec{
			PersistentVolumeReclaimPolicy: *options.StorageClass.ReclaimPolicy,
			AccessModes:                   options.PVC.Spec.AccessModes,
			Capacity: v1.ResourceList{
				v1.ResourceName(v1.ResourceStorage): options.PVC.Spec.Resources.Requests[v1.ResourceName(v1.ResourceStorage)],
			},
			StorageClassName: "efs-sc",
			PersistentVolumeSource: v1.PersistentVolumeSource{
				CSI: &v1.CSIPersistentVolumeSource{
					Driver:       efsCsiDriverName,
					VolumeHandle: fmt.Sprintf("%s:%s", p.fileSystemID, p.getRemotePath(options)),
				},
			},
			MountOptions: mountOptions,
		},
	}

	if gidAllocate {
		pv.ObjectMeta.Annotations = map[string]string{
			gidallocator.VolumeGidAnnotationKey: strconv.FormatInt(int64(*gid), 10),
		}
	}

	return pv, nil
}

func (p *efsProvisioner) createVolume(path string, gid *int) error {
	perm := os.FileMode(0777)
	if gid != nil {
		perm = os.FileMode(0771 | os.ModeSetgid)
	}

	if err := os.MkdirAll(path, perm); err != nil {
		return err
	}

	// Due to umask, need to chmod
	if err := os.Chmod(path, perm); err != nil {
		os.RemoveAll(path)
		return err
	}

	if gid != nil {
		if err := os.Chown(path, os.Getuid(), *gid); err != nil {
			os.RemoveAll(path)
			return err
		}
	}

	return nil
}

func (p *efsProvisioner) getLocalPath(options controller.ProvisionOptions) string {
	return path.Join(p.mountPoint, "/", p.subPath, "/", p.getDirectoryName(options))
}

func (p *efsProvisioner) getRemotePath(options controller.ProvisionOptions) string {
	return path.Join("/", p.subPath, "/", p.getDirectoryName(options))
}

func (p *efsProvisioner) getDirectoryName(options controller.ProvisionOptions) string {
	return options.PVC.Namespace + "-" + options.PVC.Name + "-" + options.PVName
}

// Delete removes the storage asset that was created by Provision represented
// by the given PV.
func (p *efsProvisioner) Delete(volume *v1.PersistentVolume) error {
	//TODO ignorederror
	err := p.allocator.Release(volume)
	if err != nil {
		return err
	}

	path, err := p.getLocalPathToDelete(volume.Spec.CSI)
	if err != nil {
		return err
	}

	if err := os.RemoveAll(path); err != nil {
		return err
	}

	return nil
}

func (p *efsProvisioner) getLocalPathToDelete(csi *v1.CSIPersistentVolumeSource) (string, error) {
	if csi.Driver != efsCsiDriverName {
		return "", fmt.Errorf("volume's driver %s is not %s", csi.Driver, efsCsiDriverName)
	}

	parts := strings.Split(csi.VolumeHandle, ":")
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid volumeHandle: %s", csi.VolumeHandle)
	}

	if parts[0] != p.fileSystemID {
		return "", fmt.Errorf("file system ID %s in volumeHandle doesn't match configured file system ID %s", parts[0], p.fileSystemID)
	}

	subPath := filepath.Clean(parts[1])
	prefix := path.Join("/", p.subPath) + "/"
	if !strings.HasPrefix(subPath, prefix) || subPath == prefix {
		return "", fmt.Errorf("invalid subpath %s in volume", parts[1])
	}

	return path.Join(p.mountPoint, "/", parts[1]), nil
}

func main() {
	var fileSystemID, mountPoint, subPath string
	flag.StringVar(&fileSystemID, "file-system-id", "", "the ID of the EFS file system (fs-abcdefg)")
	flag.StringVar(&mountPoint, "mountpoint", "/efs", "the path in this pod where the EFS file system is mounted")
	flag.StringVar(&subPath, "subpath", "/persistentvolumes", "the subpath in the EFS file system that will be used for persistent volumes")
	flag.Parse()
	flag.Set("logtostderr", "true")

	config, err := rest.InClusterConfig()
	if err != nil {
		klog.Fatalf("Failed to create config: %v", err)
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		klog.Fatalf("Failed to create client: %v", err)
	}

	// The controller needs to know what the server version is because out-of-tree
	// provisioners aren't officially supported until 1.5
	serverVersion, err := clientset.Discovery().ServerVersion()
	if err != nil {
		klog.Fatalf("Error getting server version: %v", err)
	}

	efsProvisioner := &efsProvisioner{
		fileSystemID: fileSystemID,
		mountPoint:   mountPoint,
		subPath:      subPath,
		allocator:    gidallocator.New(clientset),
	}

	pc := controller.NewProvisionController(
		clientset,
		provisionerName,
		efsProvisioner,
		serverVersion.GitVersion,
	)

	pc.Run(wait.NeverStop)
}
