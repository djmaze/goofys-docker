package main

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"time"

	"fmt"
	"log"
	"os"
	"strings"
	"syscall"

	"path/filepath"

	volume "github.com/docker/go-plugins-helpers/volume"
	goofys "github.com/ppenguin/goofys/api"
	common "github.com/ppenguin/goofys/api/common"
)

type s3Driver struct {
	root        string
	connections map[string]int
	volumes     map[string]map[string]string
	m           *sync.Mutex
  endpoint string
  accessKey string
  secretKey string
}

func newS3Driver(root string, endpoint string, accessKey string, secretKey string) s3Driver {
	return s3Driver{
		root:        root,
		connections: map[string]int{},
		volumes:     map[string]map[string]string{},
		m:           &sync.Mutex{},
		endpoint: endpoint,
		accessKey: accessKey,
		secretKey: secretKey,
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

	config := common.FlagStorage{
    MountPoint:       d.mountpoint(name),
  	MountOptions:     map[string]string{"allow_other": ""},
		StatCacheTTL: time.Minute,
		TypeCacheTTL: time.Minute,
	}

	s3Config := (&common.S3Config{}).Init()

	bucket := name
	if bkt, ok := d.volumes[volumeName]["bucket"]; ok {
		bucket = bkt
	}
	if prefix, ok := d.volumes[volumeName]["prefix"]; ok {
		bucket = bucket + ":" + prefix
	}
  if endpoint, ok := d.volumes[volumeName]["endpoint"]; ok {
    config.Endpoint = endpoint
  } else {
		if (d.endpoint != "") {
			config.Endpoint = d.endpoint
		}
	}
  if access_key, ok := d.volumes[volumeName]["access_key"]; ok {
    if secret_key, ok := d.volumes[volumeName]["secret_key"]; ok {
      s3Config.AccessKey = access_key
      s3Config.SecretKey = secret_key
    }
  } else {
		s3Config.AccessKey = d.accessKey
		s3Config.SecretKey = d.secretKey
	}
	if region, ok := d.volumes[volumeName]["region"]; ok {
		s3Config.Region = region
	}
	if storageClass, ok := d.volumes[volumeName]["storage-class"]; ok {
		s3Config.StorageClass = storageClass
	}
	if acl, ok := d.volumes[volumeName]["acl"]; ok {
		s3Config.ACL = acl
	} else {
		s3Config.ACL = "private"
	}
	if debugS3, ok := d.volumes[volumeName]["debugs3"]; ok {
		if s, err := strconv.ParseBool(debugS3); err == nil {
			config.DebugS3 = s
		}
	}
  if useCacheString, ok := d.volumes[volumeName]["use-cache"]; ok {
		if useCache, err := strconv.ParseBool(useCacheString); err == nil {
      if useCache {
        config.Cache = []string{config.MountPoint, "/tmp", config.MountPoint, "--free", "50%", "-oallow_other", "-ononempty"}
      }
    }
  }
  if uidString, ok := d.volumes[volumeName]["uid"]; ok {
    uid, err := strconv.ParseUint(uidString, 10, 32)
    if err != nil {
      return errors.New("uid must be uint32")
    }
    config.Uid = uint32(uid)
  }
  if gidString, ok := d.volumes[volumeName]["gid"]; ok {
    gid, err := strconv.ParseUint(gidString, 10, 32)
    if err != nil {
      return errors.New("gid must be uint32")
    }
    config.Gid = uint32(gid)
  }
  if dirModeString, ok := d.volumes[volumeName]["dir-mode"]; ok {
    dirMode, err := strconv.ParseUint(dirModeString, 8, 32)
    if err != nil {
      return errors.New("dir-mode must be given in octal format")
    }
    config.DirMode = os.FileMode(dirMode)
  } else {
    dirMode, _ := strconv.ParseUint("0755", 8, 32)
		config.DirMode = os.FileMode(dirMode)
	}
  if fileModeString, ok := d.volumes[volumeName]["file-mode"]; ok {
    fileMode, err := strconv.ParseUint(fileModeString, 8, 32)
    if err != nil {
      return errors.New("file-mode must be given in octal format")
    }
    config.FileMode = os.FileMode(fileMode)
  } else {
    fileMode, _ := strconv.ParseUint("0644", 8, 32)
		config.FileMode = os.FileMode(fileMode)
	}
  if cheapString, ok := d.volumes[volumeName]["cheap"]; ok {
		if cheap, err := strconv.ParseBool(cheapString); err == nil {
      if cheap {
				log.Printf("Cheap mode\n")
				config.Cheap = true
			}
		}
	}

	config.Backend = s3Config

	log.Printf("Create Goofys for bucket %s\n", bucket)

  _, _, err := goofys.Mount(context.Background(), bucket, &config)
  if err != nil {
		return err
  }

	log.Printf("Goofys mounted for bucket %s\n", bucket)

	return nil
}

func (d s3Driver) Capabilities() *volume.CapabilitiesResponse {
	log.Printf("Capabilities\n")
	return &volume.CapabilitiesResponse{
		Capabilities: volume.Capability{Scope: "local"},
	}
}
