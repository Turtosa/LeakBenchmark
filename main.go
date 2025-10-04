package main

import (
	"context"
	"fmt"
	"log"

	"github.com/leakbenchmark/deployer/internal/deployer"
)

func deployBenchmarkProjects() ([]string, error) {
	benchmarkPath := "./benchmark_projects"

	ctx := context.Background()
	d, err := deployer.New()
	if err != nil {
		return []string{}, fmt.Errorf("Failed to create deployer: %v", err)
	}
	defer d.Close()

	projects, err := d.DiscoverProjects(benchmarkPath)
	if err != nil {
		return []string{}, fmt.Errorf("Failed to discover projects: %v", err)
	}

	fmt.Printf("Discovered %d benchmark projects:\n", len(projects))
	for _, project := range projects {
		fmt.Printf("- %s\n", project.Name)
	}

	fmt.Println("\nStarting deployment...")
	results := d.DeployAll(ctx, projects)

	fmt.Println("\nDeployment Results:")
	containerIDs := []string{}
	for _, result := range results {
		if result.Error != nil {
			fmt.Printf("%s: %v\n", result.Project.Name, result.Error)
		} else {
			fmt.Printf("%s: Container %s running on ports %v\n",
				result.Project.Name, result.ContainerID[:12], result.Ports)
			containerIDs = append(containerIDs, result.ContainerID)
		}
	}

	return containerIDs, err
}

func main() {
	containersIDs, err := deployBenchmarkProjects()
	if err != nil {
		log.Fatal(err)
	}
	log.Println(containersIDs)
}
