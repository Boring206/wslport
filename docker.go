package main

import (
	"context"
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type container struct {
	ID    string
	Name  string
	Image string
}

// dockerFormat 用 | 分隔：容器名稱與映像名稱都不會含這個字元，也不需要任何引號。
const dockerFormat = "{{.ID}}|{{.Names}}|{{.Image}}"

func parseDockerPS(out string) []container {
	var list []container
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(strings.TrimSpace(line), "|")
		if len(f) != 3 || f[0] == "" {
			continue
		}
		list = append(list, container{ID: f[0], Name: f[1], Image: f[2]})
	}
	return list
}

// dockerPSArgs：Docker 20.10 之後 publish= 比對的是 host port。
func dockerPSArgs(port int) []string {
	return []string{"ps", "--filter", "publish=" + strconv.Itoa(port), "--format", dockerFormat}
}

// runDocker 在 Windows（distro 為空）或指定 distro 內執行 docker。
func runDocker(distro string, timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if distro != "" {
		full := append([]string{"-d", distro, "-u", "root", "-e", "docker"}, args...)
		out, err := runSystem(ctx, "", system32("wsl.exe"), full...)
		return string(out), err
	}
	exe, err := exec.LookPath("docker.exe")
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Dir = systemRoot()
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// findContainers 找出發佈了這個 host port 的容器；沒有 docker 指令時回傳 nil。
func findContainers(distro string, port int) []container {
	out, err := runDocker(distro, 5*time.Second, dockerPSArgs(port)...)
	if err != nil {
		return nil
	}
	return parseDockerPS(out)
}

func stopContainer(distro, id string) error {
	out, err := runDocker(distro, 30*time.Second, "stop", id)
	if err != nil {
		return errors.New(firstLine(out))
	}
	return nil
}

// isDockerOwner 回報這個佔用者是不是 Docker 替容器開的 port。
func isDockerOwner(o *Owner) bool {
	switch baseName(o.Name) {
	case "com.docker.backend", "docker-proxy":
		return true
	}
	return false
}
