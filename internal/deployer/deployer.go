package deployer

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
)

type Deployer struct {
	dockerClient *client.Client
}

type Project struct {
	Name       string
	Path       string
	DockerFile string
	ComposeFile string
	EnvFiles   []string
	ConfigDir  string
}

type DeploymentResult struct {
	Project     *Project
	ContainerID string
	Ports       []string
	Error       error
}

type ComposeService struct {
	Name        string
	Image       string
	Build       string
	Ports       []string
	Environment map[string]string
}

func New() (*Deployer, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("failed to create Docker client: %w", err)
	}

	return &Deployer{
		dockerClient: cli,
	}, nil
}

func (d *Deployer) Close() {
	if d.dockerClient != nil {
		d.dockerClient.Close()
	}
}

func (d *Deployer) DiscoverProjects(benchmarkPath string) ([]*Project, error) {
	var projects []*Project

	entries, err := os.ReadDir(benchmarkPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read benchmark directory: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		projectPath := filepath.Join(benchmarkPath, entry.Name())
		project, err := d.analyzeProject(entry.Name(), projectPath)
		if err != nil {
			fmt.Printf("Warning: failed to analyze project %s: %v\n", entry.Name(), err)
			continue
		}

		if project != nil {
			projects = append(projects, project)
		}
	}

	return projects, nil
}

func (d *Deployer) analyzeProject(name, path string) (*Project, error) {
	project := &Project{
		Name: name,
		Path: path,
	}

	envPatterns := []string{".env", ".env.example", ".env.local", ".env.prod.example", "stylo-example.env", ".example.env", "Backend/.env", "src/core/config.js"}
	for _, pattern := range envPatterns {
		envPath := filepath.Join(path, pattern)
		if _, err := os.Stat(envPath); err == nil {
			project.EnvFiles = append(project.EnvFiles, envPath)
		}
	}
	log.Println(name, project.EnvFiles)

	configDir := filepath.Join(path, "config")
	if _, err := os.Stat(configDir); err == nil {
		project.ConfigDir = configDir
	}

	return project, nil
}

func (d *Deployer) DeployAll(ctx context.Context, projects []*Project) []*DeploymentResult {
	results := make([]*DeploymentResult, len(projects))

	for i, project := range projects {
		result := &DeploymentResult{Project: project}

		if err := d.deployProject(ctx, project, result); err != nil {
			result.Error = err
		}

		results[i] = result
	}

	return results
}

func (d *Deployer) deployProject(ctx context.Context, project *Project, result *DeploymentResult) error {
	secrets := generateSecrets(project)

	tempDir, err := os.MkdirTemp("", fmt.Sprintf("benchmark-%s-", project.Name))
	if err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tempDir)

	if err := d.prepareProjectFiles(project, tempDir, secrets); err != nil {
		return fmt.Errorf("failed to prepare project files: %w", err)
	}

	return d.deployWithBlankContainer(ctx, project, tempDir, result)
}

func (d *Deployer) createBuildContext(dir string) (io.ReadCloser, error) {
	pr, pw := io.Pipe()

	go func() {
		defer pw.Close()
		tw := tar.NewWriter(pw)
		defer tw.Close()

		err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			relPath, err := filepath.Rel(dir, path)
			if err != nil {
				return err
			}

			if strings.Contains(relPath, ".git") {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}

			header, err := tar.FileInfoHeader(info, "")
			if err != nil {
				return err
			}
			header.Name = relPath

			if err := tw.WriteHeader(header); err != nil {
				return err
			}

			if info.Mode().IsRegular() {
				file, err := os.Open(path)
				if err != nil {
					return err
				}
				defer file.Close()

				_, err = io.Copy(tw, file)
				return err
			}

			return nil
		})

		if err != nil {
			pw.CloseWithError(err)
		}
	}()

	return pr, nil
}

func (d *Deployer) deployWithBlankContainer(ctx context.Context, project *Project, tempDir string, result *DeploymentResult) error {
	baseImage := "node:22"
	fmt.Printf("Using base image: %s\n", baseImage)

	fmt.Printf("Pulling base image %s...\n", baseImage)
	pullReader, err := d.dockerClient.ImagePull(ctx, baseImage, types.ImagePullOptions{})
	if err != nil {
		return fmt.Errorf("failed to pull base image: %w", err)
	}
	defer pullReader.Close()
	io.Copy(os.Stdout, pullReader)

	containerName := fmt.Sprintf("benchmark-%s-%s", project.Name, generateRandomString(8))

	containerConfig := &container.Config{
		Image:        baseImage,
		WorkingDir:   "/app",
		Cmd:          []string{"sh", "-c", "sleep infinity"},
	}

	hostConfig := &container.HostConfig{
		AutoRemove:   false,
	}

	fmt.Printf("Creating blank container %s...\n", containerName)
	resp, err := d.dockerClient.ContainerCreate(ctx, containerConfig, hostConfig, &network.NetworkingConfig{}, nil, containerName)
	if err != nil {
		return fmt.Errorf("failed to create container: %w", err)
	}

	fmt.Printf("Starting container %s...\n", resp.ID[:12])
	if err := d.dockerClient.ContainerStart(ctx, resp.ID, types.ContainerStartOptions{}); err != nil {
		return fmt.Errorf("failed to start container: %w", err)
	}

	time.Sleep(3 * time.Second)

	if err := d.copyFilesToContainer(ctx, resp.ID, tempDir); err != nil {
		return fmt.Errorf("failed to copy files to container: %w", err)
	}

	result.ContainerID = resp.ID

	fmt.Printf("Container %s deployed successfully\n", resp.ID[:12])
	return nil
}

func (d *Deployer) copyFilesToContainer(ctx context.Context, containerID, sourceDir string) error {
	fmt.Printf("Copying project files to container...\n")

	tarReader, err := d.createBuildContext(sourceDir)
	if err != nil {
		return fmt.Errorf("failed to create tar archive: %w", err)
	}
	defer tarReader.Close()

	return d.dockerClient.CopyToContainer(ctx, containerID, "/app", tarReader, types.CopyToContainerOptions{})
}
