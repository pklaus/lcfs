// +build linux

package dfs

import (
    "fmt"
    "path"
    "syscall"
    "unsafe"

    "github.com/docker/docker/daemon/graphdriver"
    "github.com/docker/docker/pkg/idtools"
    "github.com/opencontainers/runc/libcontainer/label"
)

// Copied from dfs.h
const (
    SNAP_CREATE = 1
    CLONE_CREATE = 2
    SNAP_REMOVE = 3
    SNAP_MOUNT = 4
    SNAP_UMOUNT = 5
    SNAP_STAT = 6
    UMOUNT_ALL = 7
)

func init() {
    graphdriver.Register("dfs", Init)
}

// Init returns a new DFS driver.
// An error is returned if DFS is not supported.
func Init(home string, options []string, uidMaps, gidMaps []idtools.IDMap) (graphdriver.Driver, error) {
    rootUID, rootGID, err := idtools.GetRootUIDGID(uidMaps, gidMaps)
    if err != nil {
        return nil, err
    }
    if err := idtools.MkdirAllAs(home, 0700, rootUID, rootGID); err != nil {
        return nil, err
    }

    driver := &Driver{
        home:    home,
        uidMaps: uidMaps,
        gidMaps: gidMaps,
    }

    return graphdriver.NewNaiveDiffDriver(driver, uidMaps, gidMaps), nil
}


// Driver contains information about the filesystem mounted.
type Driver struct {
    //root of the file system
    home    string
    uidMaps []idtools.IDMap
    gidMaps []idtools.IDMap
}

// String prints the name of the driver (dfs).
func (d *Driver) String() string {
    return "dfs"
}

// Status returns current driver information in a two dimensional string array.
// Output contains "Build Version" and "Library Version" of the dfs libraries used.
// Version information can be used to check compatibility with your kernel.
func (d *Driver) Status() [][2]string {
    status := [][2]string{}
    if bv := dfsBuildVersion(); bv != "-" {
        status = append(status, [2]string{"Build Version", bv})
    }
    if lv := dfsLibVersion(); lv != -1 {
        status = append(status, [2]string{"Library Version", fmt.Sprintf("%d", lv)})
    }
    return status
}

// GetMetadata returns empty metadata for this driver.
func (d *Driver) GetMetadata(id string) (map[string]string, error) {
    return nil, nil
}

// Issue ioctl for various operations
func (d *Driver) ioctl(cmd int, parent, id string) error {
    var op, arg uintptr
    var name string
    var plen int

    // Open snapshot root directory
    fd, err := syscall.Open(d.home, syscall.O_DIRECTORY, 0);
    if err != nil {
        return err
    }

    // Create a name string which includes both parent and id 
    if parent != "" {
        name = path.Join(parent, id)
        plen = len(parent)
    } else {
        name = id
    }
    if name == "" {
        op = uintptr(cmd)
    } else {
        op = uintptr((1 << 30) | (len(name) << 16) | (plen << 8) | cmd);
        arg = uintptr(unsafe.Pointer(&[]byte(name)[0]))
    }
    _, _, ep := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), op, arg);
    err = syscall.Close(fd)
    if ep != 0 {
        return syscall.Errno(ep)
    }
    return err
}

// Cleanup unmounts the home directory.
func (d *Driver) Cleanup() error {
    return d.ioctl(UMOUNT_ALL, "", "")
}

// Create a file system with the given id
func (d *Driver) create(id, parent, mountLabel string, rw bool,
                        storageOpt map[string]string) error {
    var err error

    if rw {
        err = d.ioctl(CLONE_CREATE, parent, id)
    } else {
        err = d.ioctl(SNAP_CREATE, parent, id)
    }
    if err != nil {
        return err
    }
    file := path.Join(d.home, id)
    return label.Relabel(file, mountLabel, false)
}

// CreateReadWrite creates a layer that is writable for use as a container
// file system.
func (d *Driver) CreateReadWrite(id, parent, mountLabel string, storageOpt map[string]string) error {
    return d.create(id, parent, mountLabel, true, storageOpt)
}

// Create the filesystem with given id.
func (d *Driver) Create(id, parent, mountLabel string, storageOpt map[string]string) error {
    return d.create(id, parent, mountLabel, false, storageOpt)
}

// Remove the filesystem with given id.
func (d *Driver) Remove(id string) error {
    return d.ioctl(SNAP_REMOVE, "", id)
}

// Get the requested filesystem id.
func (d *Driver) Get(id, mountLabel string) (string, error) {
    dir := path.Join(d.home, id)
    err := d.ioctl(SNAP_MOUNT, "", id)
    if err != nil {
        return "", err
    }
    return dir, nil
}

// Put is kind of unmounting the file system.
func (d *Driver) Put(id string) error {
    return d.ioctl(SNAP_UMOUNT, "", id)
}

// Exists checks if the id exists in the filesystem.
func (d *Driver) Exists(id string) bool {
    err := d.ioctl(SNAP_STAT, "", id)
    return err == nil
}
