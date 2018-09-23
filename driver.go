package main

import (
  "context"
  "errors"
	"strconv"
	"sync"

	"fmt"
	"log"
	"os"
	"strings"
	"syscall"

	"path/filepath"

	goofys "github.com/kahing/goofys/api"
	volume "github.com/docker/go-plugins-helpers/volume"
)

type s3Driver struct {
	root        string
	connections map[string]int
	volumes     map[string]map[string]string
	m           *sync.Mutex
}

func newS3Driver(root string) s3Driver {
	return s3Driver{
		root:        root,
		connections: map[string]int{},
		volumes:     map[string]map[string]string{},
		m:           &sync.Mutex{},
	}
}

func (d s3Driver) Create(r *volume.CreateRequest) error {
	log.Printf("Creating volume %s\n", r.Name)
	d.m.Lock()
	defer d.m.Unlock()
	d.volumes[r.Name] = r.Options
	return nil
}

func (d s3Driver) Get(r *volume.GetRequest) (*volume.GetResponse, error) {
	d.m.Lock()
	defer d.m.Unlock()
	if _, exists := d.volumes[r.Name]; exists {
		return &volume.GetResponse{
			Volume: &volume.Volume{
				Name:       r.Name,
				Mountpoint: d.mountpoint(r.Name),
			},
		}, nil
	}
	return nil, errors.New(fmt.Sprintf("Unable to find volume mounted on %s", d.mountpoint(r.Name)))
}

func (d s3Driver) List() (*volume.ListResponse, error) {
	d.m.Lock()
	defer d.m.Unlock()
	var volumes []*volume.Volume
	for k := range d.volumes {
		volumes = append(volumes, &volume.Volume{
			Name:       k,
			Mountpoint: d.mountpoint(k),
		})
	}
	return &volume.ListResponse{
		Volumes: volumes,
	}, nil
}

func (d s3Driver) Remove(r *volume.RemoveRequest) error {
	log.Printf("Removing volume %s\n", r.Name)
	d.m.Lock()
	defer d.m.Unlock()
	bucket := strings.SplitN(r.Name, "/", 2)[0]

	count, exists := d.connections[bucket]
	if exists && count < 1 {
		delete(d.connections, bucket)
	}
	delete(d.volumes, r.Name)
	return nil
}

func (d s3Driver) Path(r *volume.PathRequest) (*volume.PathResponse, error) {
	return &volume.PathResponse{
		Mountpoint: d.mountpoint(r.Name),
	}, nil
}

func (d s3Driver) Mount(r *volume.MountRequest) (*volume.MountResponse, error) {
	d.m.Lock()
	defer d.m.Unlock()

	bucket := strings.SplitN(r.Name, "/", 2)[0]

	log.Printf("Mounting volume %s on %s\n", r.Name, d.mountpoint(bucket))

	count, exists := d.connections[bucket]
	if exists && count > 0 {
		d.connections[bucket] = count + 1
		return &volume.MountResponse{Mountpoint: d.mountpoint(r.Name)}, nil
	}

	fi, err := os.Lstat(d.mountpoint(bucket))

	if os.IsNotExist(err) {
		if err := os.MkdirAll(d.mountpoint(bucket), 0755); err != nil {
			return nil, err
		}
	} else if err != nil {
		if e, ok := err.(*os.PathError); ok && e.Err == syscall.ENOTCONN {
			// Crashed previously? Unmount
      err := goofys.TryUnmount(d.mountpoint(bucket))
      if err != nil {
        err2 := fmt.Errorf("Failed to unmount: %v", err)
        return nil, err2
      }
		} else {
			return nil, err
		}
	}

	if fi != nil && !fi.IsDir() {
		return nil, errors.New(fmt.Sprintf("%v already exist and it's not a directory", d.mountpoint(bucket)))
	}

	err = d.mountBucket(bucket, r.Name)
	if err != nil {
		return nil, err
	}

	d.connections[bucket] = 1

	return &volume.MountResponse{Mountpoint: d.mountpoint(r.Name)}, nil
}

func (d s3Driver) Unmount(r *volume.UnmountRequest) error {
	d.m.Lock()
	defer d.m.Unlock()

	bucket := strings.SplitN(r.Name, "/", 2)[0]

	log.Printf("Unmounting volume %s from %s\n", r.Name, d.mountpoint(bucket))

	if count, exists := d.connections[bucket]; exists {
		if count == 1 {
			mountpoint := d.mountpoint(bucket)
      err := goofys.TryUnmount(mountpoint)
      if err != nil {
        log.Printf("Failed to unmount: %v", err)
      }
			os.Remove(mountpoint)
		}
		d.connections[bucket] = count - 1
	} else {
		return errors.New(fmt.Sprintf("Unable to find volume mounted on %s", d.mountpoint(bucket)))
	}

	return nil
}

func (d *s3Driver) mountpoint(name string) string {
	return filepath.Join(d.root, name)
}

func (d *s3Driver) mountBucket(name string, volumeName string) error {

  config := &goofys.Config{
    MountPoint:       d.mountpoint(name),
    MountOptions:     map[string]string{"allow_other": ""},
		Region:           "us-east-1",
		StorageClass:     "STANDARD",
  }

	bucket := name
	if bkt, ok := d.volumes[volumeName]["bucket"]; ok {
		bucket = bkt
	}
	if prefix, ok := d.volumes[volumeName]["prefix"]; ok {
		bucket = bucket + ":" + prefix
	}
  if endpoint, ok := d.volumes[volumeName]["endpoint"]; ok {
    config.Endpoint = endpoint
  }
  if access_key, ok := d.volumes[volumeName]["access_key"]; ok {
    if secret_key, ok := d.volumes[volumeName]["secret_key"]; ok {
      config.AccessKey = access_key
      config.SecretKey = secret_key
    }
  }
	if region, ok := d.volumes[volumeName]["region"]; ok {
		config.Region = region
	}
	if storageClass, ok := d.volumes[volumeName]["storage-class"]; ok {
		config.StorageClass = storageClass
	}
	if debugS3, ok := d.volumes[volumeName]["debugs3"]; ok {
		if s, err := strconv.ParseBool(debugS3); err == nil {
			config.DebugS3 = s
		}
	}

	log.Printf("Create Goofys for bucket %s\n", bucket)

  _, _, err := goofys.Mount(context.TODO(), bucket, config)
  if err != nil {
		return err
  }

	return nil
}

func (d s3Driver) Capabilities() *volume.CapabilitiesResponse {
	log.Printf("Capabilities\n")
	return &volume.CapabilitiesResponse{
		Capabilities: volume.Capability{Scope: "local"},
	}
}
