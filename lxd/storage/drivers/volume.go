package drivers

import (
	"fmt"
	"os"

	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/shared"
)

// VolumeType represents a storage volume type.
type VolumeType string

// VolumeTypeImage represents an image storage volume.
const VolumeTypeImage = VolumeType("images")

// VolumeTypeCustom represents a custom storage volume.
const VolumeTypeCustom = VolumeType("custom")

// VolumeTypeContainer represents a container storage volume.
const VolumeTypeContainer = VolumeType("containers")

// VolumeTypeVM represents a virtual-machine storage volume.
const VolumeTypeVM = VolumeType("virtual-machines")

// ContentType indicates the format of the volume.
type ContentType string

// ContentTypeFS indicates the volume will be populated with a mountabble filesystem.
const ContentTypeFS = ContentType("fs")

// ContentTypeBlock indicates the volume will be a block device and its contents and we do not
// know which filesystem(s) (if any) are in use.
const ContentTypeBlock = ContentType("block")

// Volume represents a storage volume, and provides functions to mount and unmount it.
type Volume struct {
	name        string
	pool        string
	volType     VolumeType
	contentType ContentType
	config      map[string]string
	driver      Driver
}

// NewVolume instantiates a new Volume struct.
func NewVolume(driver Driver, poolName string, volType VolumeType, contentType ContentType, volName string, volConfig map[string]string) Volume {
	return Volume{
		name:        volName,
		pool:        poolName,
		volType:     volType,
		contentType: contentType,
		config:      volConfig,
		driver:      driver,
	}
}

// NewSnapshot instantiates a new Volume struct representing a snapshot of the parent volume.
func (v Volume) NewSnapshot(snapshotName string) (Volume, error) {
	if v.IsSnapshot() {
		return Volume{}, fmt.Errorf("Cannot create a snapshot volume from a snapshot")
	}

	fullSnapName := GetSnapshotVolumeName(v.name, snapshotName)
	return NewVolume(v.driver, v.pool, v.volType, v.contentType, fullSnapName, v.config), nil
}

// IsSnapshot indicates if volume is a snapshot.
func (v Volume) IsSnapshot() bool {
	return shared.IsSnapshot(v.name)
}

// MountPath returns the path where the volume will be mounted.
func (v Volume) MountPath() string {
	return GetVolumeMountPath(v.pool, v.volType, v.name)
}

// CreateMountPath creates the volume's mount path and sets the correct permission for the type.
func (v Volume) CreateMountPath() error {
	volPath := v.MountPath()

	// Create volume's mount path, with any created directories set to 0711.
	err := os.MkdirAll(volPath, 0711)
	if err != nil {
		return err
	}

	// Set very restrictive mode 0100 for non-custom and non-image volumes.
	if v.volType != VolumeTypeCustom && v.volType != VolumeTypeImage {
		// Set mode of actual volume's mount path.
		err = os.Chmod(volPath, 0100)
		if err != nil {
			return err
		}
	}

	return nil
}

// MountTask runs the supplied task after mounting the volume if needed. If the volume was mounted
// for this then it is unmounted when the task finishes.
func (v Volume) MountTask(task func(mountPath string, op *operations.Operation) error, op *operations.Operation) error {
	parentName, snapName, isSnap := shared.ContainerGetParentAndSnapshotName(v.name)

	mountLockID := fmt.Sprintf("mount/%s/%s", v.volType, v.name)
	umountLockID := fmt.Sprintf("umount/%s/%s", v.volType, v.name)

	// If the volume is a snapshot then call the snapshot specific mount/unmount functions as
	// these will mount the snapshot read only.
	if isSnap {
		unlock := lock(mountLockID)

		ourMount, err := v.driver.MountVolumeSnapshot(v.volType, parentName, snapName, op)
		if err != nil {
			unlock()
			return err
		}

		unlock()

		if ourMount {
			defer func() {
				unlock := lock(umountLockID)
				v.driver.UnmountVolumeSnapshot(v.volType, parentName, snapName, op)
				unlock()
			}()
		}
	} else {
		unlock := lock(mountLockID)

		ourMount, err := v.driver.MountVolume(v.volType, v.name, op)
		if err != nil {
			unlock()
			return err
		}

		unlock()

		if ourMount {
			defer func() {
				unlock := lock(umountLockID)
				v.driver.UnmountVolume(v.volType, v.name, op)
				unlock()
			}()
		}
	}

	return task(v.MountPath(), op)
}

// Snapshots returns a list of snapshots for the volume.
func (v Volume) Snapshots(op *operations.Operation) ([]Volume, error) {
	if v.IsSnapshot() {
		return nil, fmt.Errorf("Volume is a snapshot")
	}

	snapshots, err := v.driver.VolumeSnapshots(v.volType, v.name, op)
	if err != nil {
		return nil, err
	}

	snapVols := []Volume{}
	for _, snapName := range snapshots {
		snapshot, err := v.NewSnapshot(snapName)
		if err != nil {
			return nil, err
		}
		snapVols = append(snapVols, snapshot)
	}

	return snapVols, nil
}
