package main

import (
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"testing"

	v1 "k8s.io/api/core/v1"
	storage "k8s.io/api/storage/v1"
	storageapis "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/sig-storage-lib-external-provisioner/controller"
	"sigs.k8s.io/sig-storage-lib-external-provisioner/gidallocator"

	"k8s.io/client-go/kubernetes/fake"

	"github.com/stretchr/testify/assert"
)

var fakeClient = fake.NewSimpleClientset()
var provisioner = efsProvisioner{
	fileSystemID: "fs-123456",
	mountPoint:   "./efs",
	subPath:      "/persistentvolumes",
	allocator:    gidallocator.New(fakeClient),
}

var sc = &storage.StorageClass{
	ObjectMeta: metav1.ObjectMeta{
		Name: "efs-sc",
	},
}

func TestMain(m *testing.M) {
	dir, err := ioutil.TempDir("/tmp", "chroot-test")
	if err != nil {
		log.Fatal("failed to create temporary directory: ", err)
	}
	os.Chdir(dir)

	fakeClient.StorageV1().StorageClasses().Create(sc)

	defer os.RemoveAll(dir)
	os.Exit(m.Run())
}

func TestProvisionAndDelete(t *testing.T) {
	const pvName = "pvc-123456"

	mountOptions := []string{"something", "something-else"}
	retain := v1.PersistentVolumeReclaimRetain

	options := controller.ProvisionOptions{
		StorageClass: &storageapis.StorageClass{
			ReclaimPolicy: &retain,
			Parameters: map[string]string{
				"gidallocate": "false", // don't test gid allocation
			},
			MountOptions: mountOptions,
		},
		PVName: pvName,
		PVC: &v1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-pvc",
				Namespace: "my-ns",
			},
			Spec: v1.PersistentVolumeClaimSpec{
				AccessModes: []v1.PersistentVolumeAccessMode{v1.ReadWriteMany},
				Resources: v1.ResourceRequirements{
					Requests: v1.ResourceList{
						v1.ResourceName(v1.ResourceStorage): resource.Quantity{Format: "1Mi"},
					},
				},
			},
		},
		SelectedNode: &v1.Node{},
	}

	pv, err := provisioner.Provision(options)

	fakeClient.CoreV1().PersistentVolumes().Create(pv)

	assert.NoError(t, err, "Provision() failed")
	assert.Equal(t, pvName, pv.Name, "Name is not copied")
	assert.Equal(t, retain, pv.Spec.PersistentVolumeReclaimPolicy, "Reclaim policy is not copied")
	assert.Equal(t, []v1.PersistentVolumeAccessMode{v1.ReadWriteMany}, pv.Spec.AccessModes, "Access modes are not copied")
	assert.Equal(t, resource.Quantity{Format: "1Mi"}, pv.Spec.Capacity[v1.ResourceName(v1.ResourceStorage)], "Capacity is not copied")
	assert.Equal(t, "efs-sc", pv.Spec.StorageClassName, "Incorrect storage class name")
	assert.Equal(t, mountOptions, pv.Spec.MountOptions, "Mount options are not copied")
	assert.Equal(t, efsCsiDriverName, pv.Spec.PersistentVolumeSource.CSI.Driver, "Driver name in PV is incorrect")
	assert.Equal(t, "fs-123456:/persistentvolumes/my-ns-my-pvc-pvc-123456", pv.Spec.PersistentVolumeSource.CSI.VolumeHandle)

	stat, err := os.Stat("./efs/persistentvolumes/my-ns-my-pvc-pvc-123456")
	assert.NoError(t, err, "os.Stat() failed: directory for PV probably doesn't exit")
	assert.True(t, stat.IsDir(), "Is not a directory")

	err = provisioner.Delete(pv)
	assert.NoError(t, err, "Delete() failed")

	_, err = os.Stat("./efs/persistentvolumes/my-ns-my-pvc-pvc-123456")
	assert.Error(t, err, "Directory for PV was not deleted")
	assert.True(t, os.IsNotExist(err), "error is not a NotExist error")

	_, err = os.Stat("./efs/persistentvolumes")
	assert.NoError(t, err, "mountPoint/subPath was deleted")
	assert.True(t, stat.IsDir(), "Is not a directory")
}

func TestGetLocalPathToDelete(t *testing.T) {
	_, err := provisioner.getLocalPathToDelete(&v1.CSIPersistentVolumeSource{
		Driver:       "efs.something",
		VolumeHandle: "fs-123456",
	})
	assert.Error(t, err, "Wrong driver string is ignored")

	for _, fsID := range []string{
		"fs-12345",
		"fs-123456:",
		"fs-123456:/",
		"fs-123456:/persistentvolumes",
		"fs-123456:/persistentvolumes/",
		"fs-123456:/persistentvolumes/..",
		"fs-123456:/persistentvolumes/../",
	} {
		_, err = provisioner.getLocalPathToDelete(&v1.CSIPersistentVolumeSource{
			Driver:       efsCsiDriverName,
			VolumeHandle: fsID,
		})
		assert.Error(t, err, "Wrong filesystem ID string is ignored: %s", fsID)
		fmt.Println(err)
	}

	path, err := provisioner.getLocalPathToDelete(&v1.CSIPersistentVolumeSource{
		Driver:       efsCsiDriverName,
		VolumeHandle: "fs-123456:/persistentvolumes/something",
	})
	assert.NoError(t, err)
	assert.Equal(t, "efs/persistentvolumes/something", path)
}
