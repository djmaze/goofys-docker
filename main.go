package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/docker/go-plugins-helpers/volume"
	_ "golang.org/x/net/context"
)

const (
	socketAddress = "/run/docker/plugins/goofys.sock"
)

var (
	defaultPath = filepath.Join(volume.DefaultDockerRootDirectory, "goofys")
	root        = flag.String("root", defaultPath, "Docker volumes root directory")
)

func main() {
	flag.Parse()

  endpoint := os.Getenv("ENDPOINT")
  accessKey := os.Getenv("ACCESS_KEY")
  secretKey := os.Getenv("SECRET_KEY")

	d := newS3Driver(*root, endpoint, accessKey, secretKey)
	h := volume.NewHandler(d)

	fmt.Printf("Listening on %s\n", socketAddress)
	fmt.Println(h.ServeUnix(socketAddress, syscall.Getgid()))
}
