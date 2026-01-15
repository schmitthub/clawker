package whail

import (
	"context"
	"testing"
)

func TestHealthCheck(t *testing.T) {
	ctx := context.Background()

	err := testEngine.HealthCheck(ctx)
	if err != nil {
		t.Errorf("HealthCheck failed: %v", err)
	}
}

func TestManagedLabelAccessors(t *testing.T) {
	key := testEngine.ManagedLabelKey()
	if key != testLabelPrefix+".managed" {
		t.Errorf("ManagedLabelKey() = %q, want %q", key, testLabelPrefix+".managed")
	}

	value := testEngine.ManagedLabelValue()
	if value != "true" {
		t.Errorf("ManagedLabelValue() = %q, want %q", value, "true")
	}
}

// func TestIsImageManaged(t *testing.T) {
// 	ctx := context.Background()

// 	managed, err := testEngine.IsImageManaged(ctx, testImageTag)
// 	if err != nil {
// 		t.Fatalf("IsImageManaged failed for managed image: %v", err)
// 	}
// 	if !managed {
// 		t.Error("IsImageManaged should return true for managed image")
// 	}

// 	managed, err = testEngine.IsImageManaged(ctx, unmanagedTag)
// 	if err != nil {
// 		t.Fatalf("IsImageManaged failed for unmanaged image: %v", err)
// 	}
// 	if managed {
// 		t.Error("IsImageManaged should return false for unmanaged image")
// 	}
// }

// func TestImageExists(t *testing.T) {
// 	ctx := context.Background()

// 	exists, err := testEngine.ImageExists(ctx, testImageTag)
// 	if err != nil {
// 		t.Fatalf("ImageExists failed for managed image: %v", err)
// 	}
// 	if !exists {
// 		t.Error("ImageExists should return true for managed image")
// 	}

// 	exists, err = testEngine.ImageExists(ctx, unmanagedTag)
// 	if err != nil {
// 		t.Fatalf("ImageExists failed for unmanaged image: %v", err)
// 	}
// 	if !exists {
// 		t.Error("ImageExists should return true for unmanaged image")
// 	}

// 	exists, err = testEngine.ImageExists(ctx, "nonexistent:image")
// 	if err != nil {
// 		t.Fatalf("ImageExists failed for nonexistent image: %v", err)
// 	}
// 	if exists {
// 		t.Error("ImageExists should return false for nonexistent image")
// 	}
// }

// func TestImageInspect_ReturnsLabels(t *testing.T) {
// 	ctx := context.Background()

// 	info, err := testEngine.ImageInspect(ctx, testImageTag)
// 	if err != nil {
// 		t.Fatalf("ImageInspect failed: %v", err)
// 	}

// 	if info.Config.Labels[testLabelPrefix+".managed"] != "true" {
// 		t.Errorf("Image should have managed label, got labels: %v", info.Config.Labels)
// 	}
// }

// func TestImageListByLabels(t *testing.T) {
// 	ctx := context.Background()

// 	images, err := testEngine.ImageListByLabels(ctx, map[string]string{
// 		testLabelPrefix + ".purpose": "test",
// 	})
// 	if err != nil {
// 		t.Fatalf("ImageListByLabels failed: %v", err)
// 	}

// 	found := false
// 	for _, img := range images {
// 		for _, tag := range img.RepoTags {
// 			if tag == testImageTag {
// 				found = true
// 			}
// 		}
// 	}

// 	if !found {
// 		t.Error("ImageListByLabels should find image with matching labels")
// 	}

// 	images, err = testEngine.ImageListByLabels(ctx, map[string]string{
// 		testLabelPrefix + ".nonexistent": "value",
// 	})
// 	if err != nil {
// 		t.Fatalf("ImageListByLabels failed: %v", err)
// 	}

// 	for _, img := range images {
// 		for _, tag := range img.RepoTags {
// 			if tag == testImageTag {
// 				t.Error("ImageListByLabels should not find image without matching labels")
// 			}
// 		}
// 	}
// }

// func TestContainerCreate_AppliesManagedLabels(t *testing.T) {
// 	ctx := context.Background()

// 	containerName := fmt.Sprintf("whail-test-container-%d", time.Now().UnixNano())

// 	resp, err := testEngine.ContainerCreate(ctx, &container.Config{
// 		Image: testImageBase,
// 		Cmd:   []string{"echo", "test"},
// 	}, nil, containerName)
// 	if err != nil {
// 		t.Fatalf("ContainerCreate failed: %v", err)
// 	}

// 	defer testEngine.ContainerRemove(ctx, resp.ID, true)

// 	info, err := testEngine.ContainerInspect(ctx, resp.ID)
// 	if err != nil {
// 		t.Fatalf("ContainerInspect failed: %v", err)
// 	}

// 	if info.Config.Labels[testLabelPrefix+".managed"] != "true" {
// 		t.Errorf("Container should have managed label, got: %v", info.Config.Labels)
// 	}
// }

// func TestContainerList_OnlyReturnsManagedContainers(t *testing.T) {
// 	ctx := context.Background()

// 	timestamp := time.Now().UnixNano()
// 	managedName := fmt.Sprintf("whail-test-managed-%d", timestamp)
// 	unmanagedName := fmt.Sprintf("whail-test-unmanaged-%d", timestamp)

// 	managedResp, err := testEngine.ContainerCreate(ctx, &container.Config{
// 		Image: testImageBase,
// 		Cmd:   []string{"sleep", "60"},
// 	}, nil, managedName)
// 	if err != nil {
// 		t.Fatalf("Failed to create managed container: %v", err)
// 	}
// 	defer testEngine.ContainerRemove(ctx, managedResp.ID, true)

// 	cli := testEngine.Client()
// 	unmanagedResp, err := cli.ContainerCreate(ctx, &container.Config{
// 		Image:  testImageBase,
// 		Cmd:    []string{"sleep", "60"},
// 		Labels: map[string]string{"unmanaged": "true"},
// 	}, nil, nil, nil, unmanagedName)
// 	if err != nil {
// 		t.Fatalf("Failed to create unmanaged container: %v", err)
// 	}
// 	defer cli.ContainerRemove(ctx, unmanagedResp.ID, container.RemoveOptions{Force: true})

// 	containers, err := testEngine.ContainerList(ctx, container.ListOptions{All: true})
// 	if err != nil {
// 		t.Fatalf("ContainerList failed: %v", err)
// 	}

// 	foundManaged := false
// 	foundUnmanaged := false

// 	for _, c := range containers {
// 		for _, name := range c.Names {
// 			if strings.Contains(name, managedName) {
// 				foundManaged = true
// 			}
// 			if strings.Contains(name, unmanagedName) {
// 				foundUnmanaged = true
// 			}
// 		}
// 	}

// 	if !foundManaged {
// 		t.Error("ContainerList should return managed container")
// 	}
// 	if foundUnmanaged {
// 		t.Error("ContainerList should NOT return unmanaged container")
// 	}
// }

// func TestVolumeCreate_AppliesManagedLabels(t *testing.T) {
// 	ctx := context.Background()

// 	volumeName := fmt.Sprintf("whail-test-volume-%d", time.Now().UnixNano())

// 	vol, err := testEngine.VolumeCreate(ctx, volumeName)
// 	if err != nil {
// 		t.Fatalf("VolumeCreate failed: %v", err)
// 	}

// 	defer testEngine.VolumeRemove(ctx, volumeName, true)

// 	if vol.Labels[testLabelPrefix+".managed"] != "true" {
// 		t.Errorf("Volume should have managed label, got: %v", vol.Labels)
// 	}
// }

// func TestVolumeList_OnlyReturnsManagedVolumes(t *testing.T) {
// 	ctx := context.Background()

// 	timestamp := time.Now().UnixNano()
// 	managedName := fmt.Sprintf("whail-test-managed-vol-%d", timestamp)
// 	unmanagedName := fmt.Sprintf("whail-test-unmanaged-vol-%d", timestamp)

// 	_, err := testEngine.VolumeCreate(ctx, managedName)
// 	if err != nil {
// 		t.Fatalf("Failed to create managed volume: %v", err)
// 	}
// 	defer testEngine.VolumeRemove(ctx, managedName, true)

// 	cli := testEngine.Client()
// 	_, err = cli.VolumeCreate(ctx, volume.CreateOptions{
// 		Name:   unmanagedName,
// 		Labels: map[string]string{"unmanaged": "true"},
// 	})
// 	if err != nil {
// 		t.Fatalf("Failed to create unmanaged volume: %v", err)
// 	}
// 	defer cli.VolumeRemove(ctx, unmanagedName, true)

// 	volumes, err := testEngine.VolumeList(ctx)
// 	if err != nil {
// 		t.Fatalf("VolumeList failed: %v", err)
// 	}

// 	foundManaged := false
// 	foundUnmanaged := false

// 	for _, v := range volumes.Volumes {
// 		if v.Name == managedName {
// 			foundManaged = true
// 		}
// 		if v.Name == unmanagedName {
// 			foundUnmanaged = true
// 		}
// 	}

// 	if !foundManaged {
// 		t.Error("VolumeList should return managed volume")
// 	}
// 	if foundUnmanaged {
// 		t.Error("VolumeList should NOT return unmanaged volume")
// 	}
// }

// func TestNetworkCreate_AppliesManagedLabels(t *testing.T) {
// 	ctx := context.Background()

// 	networkName := fmt.Sprintf("whail-test-network-%d", time.Now().UnixNano())

// 	resp, err := testEngine.NetworkCreate(ctx, networkName)
// 	if err != nil {
// 		t.Fatalf("NetworkCreate failed: %v", err)
// 	}

// 	defer testEngine.NetworkRemove(ctx, resp.ID)

// 	info, err := testEngine.NetworkInspect(ctx, networkName)
// 	if err != nil {
// 		t.Fatalf("NetworkInspect failed: %v", err)
// 	}

// 	if info.Labels[testLabelPrefix+".managed"] != "true" {
// 		t.Errorf("Network should have managed label, got: %v", info.Labels)
// 	}
// }

// func TestNetworkList_OnlyReturnsManagedNetworks(t *testing.T) {
// 	ctx := context.Background()

// 	timestamp := time.Now().UnixNano()
// 	managedName := fmt.Sprintf("whail-test-managed-net-%d", timestamp)
// 	unmanagedName := fmt.Sprintf("whail-test-unmanaged-net-%d", timestamp)

// 	managedResp, err := testEngine.NetworkCreate(ctx, managedName)
// 	if err != nil {
// 		t.Fatalf("Failed to create managed network: %v", err)
// 	}
// 	defer testEngine.NetworkRemove(ctx, managedResp.ID)

// 	cli := testEngine.Client()
// 	unmanagedResp, err := cli.NetworkCreate(ctx, unmanagedName, network.CreateOptions{
// 		Labels: map[string]string{"unmanaged": "true"},
// 	})
// 	if err != nil {
// 		t.Fatalf("Failed to create unmanaged network: %v", err)
// 	}
// 	defer cli.NetworkRemove(ctx, unmanagedResp.ID)

// 	networks, err := testEngine.NetworkList(ctx)
// 	if err != nil {
// 		t.Fatalf("NetworkList failed: %v", err)
// 	}

// 	foundManaged := false
// 	foundUnmanaged := false

// 	for _, n := range networks {
// 		if n.Name == managedName {
// 			foundManaged = true
// 		}
// 		if n.Name == unmanagedName {
// 			foundUnmanaged = true
// 		}
// 	}

// 	if !foundManaged {
// 		t.Error("NetworkList should return managed network")
// 	}
// 	if foundUnmanaged {
// 		t.Error("NetworkList should NOT return unmanaged network")
// 	}
// }

// func TestEnsureNetwork(t *testing.T) {
// 	ctx := context.Background()

// 	networkName := fmt.Sprintf("whail-test-ensure-net-%d", time.Now().UnixNano())

// 	// First call should create the network
// 	id1, err := testEngine.EnsureNetwork(ctx, networkName)
// 	if err != nil {
// 		t.Fatalf("EnsureNetwork failed on create: %v", err)
// 	}
// 	defer testEngine.NetworkRemove(ctx, id1)

// 	// Second call should return the same network
// 	id2, err := testEngine.EnsureNetwork(ctx, networkName)
// 	if err != nil {
// 		t.Fatalf("EnsureNetwork failed on existing: %v", err)
// 	}

// 	if id1 != id2 {
// 		t.Errorf("EnsureNetwork should return same ID, got %q and %q", id1, id2)
// 	}
// }

// func TestVolumeExists(t *testing.T) {
// 	ctx := context.Background()

// 	volumeName := fmt.Sprintf("whail-test-exists-vol-%d", time.Now().UnixNano())

// 	exists, err := testEngine.VolumeExists(ctx, volumeName)
// 	if err != nil {
// 		t.Fatalf("VolumeExists failed: %v", err)
// 	}
// 	if exists {
// 		t.Error("VolumeExists should return false for non-existent volume")
// 	}

// 	_, err = testEngine.VolumeCreate(ctx, volumeName)
// 	if err != nil {
// 		t.Fatalf("VolumeCreate failed: %v", err)
// 	}
// 	defer testEngine.VolumeRemove(ctx, volumeName, true)

// 	exists, err = testEngine.VolumeExists(ctx, volumeName)
// 	if err != nil {
// 		t.Fatalf("VolumeExists failed: %v", err)
// 	}
// 	if !exists {
// 		t.Error("VolumeExists should return true for existing volume")
// 	}
// }

// func TestNetworkExists(t *testing.T) {
// 	ctx := context.Background()

// 	networkName := fmt.Sprintf("whail-test-exists-net-%d", time.Now().UnixNano())

// 	exists, err := testEngine.NetworkExists(ctx, networkName)
// 	if err != nil {
// 		t.Fatalf("NetworkExists failed: %v", err)
// 	}
// 	if exists {
// 		t.Error("NetworkExists should return false for non-existent network")
// 	}

// 	resp, err := testEngine.NetworkCreate(ctx, networkName)
// 	if err != nil {
// 		t.Fatalf("NetworkCreate failed: %v", err)
// 	}
// 	defer testEngine.NetworkRemove(ctx, resp.ID)

// 	exists, err = testEngine.NetworkExists(ctx, networkName)
// 	if err != nil {
// 		t.Fatalf("NetworkExists failed: %v", err)
// 	}
// 	if !exists {
// 		t.Error("NetworkExists should return true for existing network")
// 	}
// }

// func TestFindContainerByName(t *testing.T) {
// 	ctx := context.Background()

// 	containerName := fmt.Sprintf("whail-test-find-%d", time.Now().UnixNano())

// 	// Should not find non-existent container
// 	_, err := testEngine.FindContainerByName(ctx, containerName)
// 	if err == nil {
// 		t.Error("FindContainerByName should return error for non-existent container")
// 	}

// 	// Create container
// 	resp, err := testEngine.ContainerCreate(ctx, &container.Config{
// 		Image: testImageBase,
// 		Cmd:   []string{"echo", "test"},
// 	}, nil, containerName)
// 	if err != nil {
// 		t.Fatalf("ContainerCreate failed: %v", err)
// 	}
// 	defer testEngine.ContainerRemove(ctx, resp.ID, true)

// 	// Should find existing container
// 	found, err := testEngine.FindContainerByName(ctx, containerName)
// 	if err != nil {
// 		t.Fatalf("FindContainerByName failed: %v", err)
// 	}
// 	if found.ID != resp.ID {
// 		t.Errorf("FindContainerByName returned wrong container: got %q, want %q", found.ID, resp.ID)
// 	}
// }

// func TestContainerRemove(t *testing.T) {
// 	ctx := context.Background()

// 	containerName := fmt.Sprintf("whail-test-remove-%d", time.Now().UnixNano())

// 	// Create container
// 	resp, err := testEngine.ContainerCreate(ctx, &container.Config{
// 		Image: testImageBase,
// 		Cmd:   []string{"echo", "test"},
// 	}, nil, containerName)
// 	if err != nil {
// 		t.Fatalf("ContainerCreate failed: %v", err)
// 	}

// 	// Verify it exists
// 	_, err = testEngine.FindContainerByName(ctx, containerName)
// 	if err != nil {
// 		t.Fatalf("Container should exist after create: %v", err)
// 	}

// 	// Remove it
// 	err = testEngine.ContainerRemove(ctx, resp.ID, true)
// 	if err != nil {
// 		t.Fatalf("ContainerRemove failed: %v", err)
// 	}

// 	// Verify it's gone
// 	_, err = testEngine.FindContainerByName(ctx, containerName)
// 	if err == nil {
// 		t.Error("Container should not exist after remove")
// 	}
// }

// func TestContainerRemove_RunningContainer(t *testing.T) {
// 	ctx := context.Background()

// 	containerName := fmt.Sprintf("whail-test-remove-running-%d", time.Now().UnixNano())

// 	// Create and start container
// 	resp, err := testEngine.ContainerCreate(ctx, &container.Config{
// 		Image: testImageBase,
// 		Cmd:   []string{"sleep", "60"},
// 	}, nil, containerName)
// 	if err != nil {
// 		t.Fatalf("ContainerCreate failed: %v", err)
// 	}

// 	err = testEngine.ContainerStart(ctx, resp.ID)
// 	if err != nil {
// 		t.Fatalf("ContainerStart failed: %v", err)
// 	}

// 	// Remove without force should fail
// 	err = testEngine.ContainerRemove(ctx, resp.ID, false)
// 	if err == nil {
// 		t.Error("ContainerRemove should fail for running container without force")
// 	}

// 	// Remove with force should succeed
// 	err = testEngine.ContainerRemove(ctx, resp.ID, true)
// 	if err != nil {
// 		t.Fatalf("ContainerRemove with force failed: %v", err)
// 	}

// 	// Verify it's gone
// 	_, err = testEngine.FindContainerByName(ctx, containerName)
// 	if err == nil {
// 		t.Error("Container should not exist after force remove")
// 	}
// }

// func TestVolumeRemove(t *testing.T) {
// 	ctx := context.Background()

// 	volumeName := fmt.Sprintf("whail-test-remove-vol-%d", time.Now().UnixNano())

// 	// Create volume
// 	_, err := testEngine.VolumeCreate(ctx, volumeName)
// 	if err != nil {
// 		t.Fatalf("VolumeCreate failed: %v", err)
// 	}

// 	// Verify it exists
// 	exists, err := testEngine.VolumeExists(ctx, volumeName)
// 	if err != nil {
// 		t.Fatalf("VolumeExists failed: %v", err)
// 	}
// 	if !exists {
// 		t.Fatal("Volume should exist after create")
// 	}

// 	// Remove it
// 	err = testEngine.VolumeRemove(ctx, volumeName, false)
// 	if err != nil {
// 		t.Fatalf("VolumeRemove failed: %v", err)
// 	}

// 	// Verify it's gone
// 	exists, err = testEngine.VolumeExists(ctx, volumeName)
// 	if err != nil {
// 		t.Fatalf("VolumeExists failed: %v", err)
// 	}
// 	if exists {
// 		t.Error("Volume should not exist after remove")
// 	}
// }

// func TestVolumeRemove_InUse(t *testing.T) {
// 	ctx := context.Background()

// 	timestamp := time.Now().UnixNano()
// 	volumeName := fmt.Sprintf("whail-test-remove-vol-inuse-%d", timestamp)
// 	containerName := fmt.Sprintf("whail-test-vol-user-%d", timestamp)

// 	// Create volume
// 	_, err := testEngine.VolumeCreate(ctx, volumeName)
// 	if err != nil {
// 		t.Fatalf("VolumeCreate failed: %v", err)
// 	}

// 	// Create container using the volume
// 	resp, err := testEngine.ContainerCreate(ctx, &container.Config{
// 		Image: testImageBase,
// 		Cmd:   []string{"sleep", "60"},
// 	}, &container.HostConfig{
// 		Binds: []string{volumeName + ":/data"},
// 	}, containerName)
// 	if err != nil {
// 		t.Fatalf("ContainerCreate failed: %v", err)
// 	}

// 	err = testEngine.ContainerStart(ctx, resp.ID)
// 	if err != nil {
// 		t.Fatalf("ContainerStart failed: %v", err)
// 	}

// 	// Remove volume without force should fail (volume in use)
// 	err = testEngine.VolumeRemove(ctx, volumeName, false)
// 	if err == nil {
// 		t.Error("VolumeRemove should fail for volume in use without force")
// 	}

// 	// Cleanup container first
// 	testEngine.ContainerRemove(ctx, resp.ID, true)

// 	// Now volume removal should succeed
// 	err = testEngine.VolumeRemove(ctx, volumeName, false)
// 	if err != nil {
// 		t.Fatalf("VolumeRemove should succeed after container removed: %v", err)
// 	}
// }

// func TestNetworkRemove(t *testing.T) {
// 	ctx := context.Background()

// 	networkName := fmt.Sprintf("whail-test-remove-net-%d", time.Now().UnixNano())

// 	// Create network
// 	resp, err := testEngine.NetworkCreate(ctx, networkName)
// 	if err != nil {
// 		t.Fatalf("NetworkCreate failed: %v", err)
// 	}

// 	// Verify it exists
// 	exists, err := testEngine.NetworkExists(ctx, networkName)
// 	if err != nil {
// 		t.Fatalf("NetworkExists failed: %v", err)
// 	}
// 	if !exists {
// 		t.Fatal("Network should exist after create")
// 	}

// 	// Remove it
// 	err = testEngine.NetworkRemove(ctx, resp.ID)
// 	if err != nil {
// 		t.Fatalf("NetworkRemove failed: %v", err)
// 	}

// 	// Verify it's gone
// 	exists, err = testEngine.NetworkExists(ctx, networkName)
// 	if err != nil {
// 		t.Fatalf("NetworkExists failed: %v", err)
// 	}
// 	if exists {
// 		t.Error("Network should not exist after remove")
// 	}
// }

// func TestImageRemove(t *testing.T) {
// 	ctx := context.Background()

// 	// Build a temporary image for this test
// 	imageTag := fmt.Sprintf("whail-test-remove-img:%d", time.Now().UnixNano())

// 	cli := testEngine.Client()
// 	dockerfile := "FROM " + testImageBase + "\nCMD [\"echo\", \"remove-test\"]\n"

// 	tarBuf := new(bytes.Buffer)
// 	createTarWithDockerfile(tarBuf, dockerfile)

// 	buildResp, err := cli.ImageBuild(ctx, tarBuf, types.ImageBuildOptions{
// 		Tags:       []string{imageTag},
// 		Labels:     map[string]string{testLabelPrefix + ".managed": "true"},
// 		Dockerfile: "Dockerfile",
// 		Remove:     true,
// 	})
// 	if err != nil {
// 		t.Fatalf("ImageBuild failed: %v", err)
// 	}
// 	defer buildResp.Body.Close()
// 	buf := new(bytes.Buffer)
// 	buf.ReadFrom(buildResp.Body)

// 	// Verify it exists
// 	exists, err := testEngine.ImageExists(ctx, imageTag)
// 	if err != nil {
// 		t.Fatalf("ImageExists failed: %v", err)
// 	}
// 	if !exists {
// 		t.Fatal("Image should exist after build")
// 	}

// 	// Remove it
// 	err = testEngine.ImageRemove(ctx, imageTag, false)
// 	if err != nil {
// 		t.Fatalf("ImageRemove failed: %v", err)
// 	}

// 	// Verify it's gone
// 	exists, err = testEngine.ImageExists(ctx, imageTag)
// 	if err != nil {
// 		t.Fatalf("ImageExists failed: %v", err)
// 	}
// 	if exists {
// 		t.Error("Image should not exist after remove")
// 	}
// }

// func TestImageRemove_InUse(t *testing.T) {
// 	ctx := context.Background()

// 	timestamp := time.Now().UnixNano()
// 	imageTag := fmt.Sprintf("whail-test-remove-img-inuse:%d", timestamp)
// 	containerName := fmt.Sprintf("whail-test-img-user-%d", timestamp)

// 	// Build a temporary image
// 	cli := testEngine.Client()
// 	dockerfile := "FROM " + testImageBase + "\nCMD [\"sleep\", \"60\"]\n"

// 	tarBuf := new(bytes.Buffer)
// 	createTarWithDockerfile(tarBuf, dockerfile)

// 	buildResp, err := cli.ImageBuild(ctx, tarBuf, types.ImageBuildOptions{
// 		Tags:       []string{imageTag},
// 		Labels:     map[string]string{testLabelPrefix + ".managed": "true"},
// 		Dockerfile: "Dockerfile",
// 		Remove:     true,
// 	})
// 	if err != nil {
// 		t.Fatalf("ImageBuild failed: %v", err)
// 	}
// 	defer buildResp.Body.Close()
// 	buf := new(bytes.Buffer)
// 	buf.ReadFrom(buildResp.Body)

// 	// Create container using the image
// 	resp, err := testEngine.ContainerCreate(ctx, &container.Config{
// 		Image: imageTag,
// 		Cmd:   []string{"sleep", "60"},
// 	}, nil, containerName)
// 	if err != nil {
// 		t.Fatalf("ContainerCreate failed: %v", err)
// 	}

// 	// Remove image without force should fail (image in use)
// 	err = testEngine.ImageRemove(ctx, imageTag, false)
// 	if err == nil {
// 		t.Error("ImageRemove should fail for image in use without force")
// 	}

// 	// Cleanup container
// 	testEngine.ContainerRemove(ctx, resp.ID, true)

// 	// Now image removal should succeed
// 	err = testEngine.ImageRemove(ctx, imageTag, false)
// 	if err != nil {
// 		t.Fatalf("ImageRemove should succeed after container removed: %v", err)
// 	}
// }

// func TestContainerStop(t *testing.T) {
// 	ctx := context.Background()

// 	containerName := fmt.Sprintf("whail-test-stop-%d", time.Now().UnixNano())

// 	// Create and start container
// 	resp, err := testEngine.ContainerCreate(ctx, &container.Config{
// 		Image: testImageBase,
// 		Cmd:   []string{"sleep", "60"},
// 	}, nil, containerName)
// 	if err != nil {
// 		t.Fatalf("ContainerCreate failed: %v", err)
// 	}
// 	defer testEngine.ContainerRemove(ctx, resp.ID, true)

// 	err = testEngine.ContainerStart(ctx, resp.ID)
// 	if err != nil {
// 		t.Fatalf("ContainerStart failed: %v", err)
// 	}

// 	// Verify it's running
// 	info, err := testEngine.ContainerInspect(ctx, resp.ID)
// 	if err != nil {
// 		t.Fatalf("ContainerInspect failed: %v", err)
// 	}
// 	if !info.State.Running {
// 		t.Error("Container should be running after start")
// 	}

// 	// Stop it
// 	timeout := 5
// 	err = testEngine.ContainerStop(ctx, resp.ID, &timeout)
// 	if err != nil {
// 		t.Fatalf("ContainerStop failed: %v", err)
// 	}

// 	// Verify it's stopped
// 	info, err = testEngine.ContainerInspect(ctx, resp.ID)
// 	if err != nil {
// 		t.Fatalf("ContainerInspect failed: %v", err)
// 	}
// 	if info.State.Running {
// 		t.Error("Container should not be running after stop")
// 	}
// }

// func TestContainerListByLabels(t *testing.T) {
// 	ctx := context.Background()

// 	timestamp := time.Now().UnixNano()
// 	containerName := fmt.Sprintf("whail-test-list-labels-%d", timestamp)

// 	// Create container with extra label
// 	resp, err := testEngine.ContainerCreate(ctx, &container.Config{
// 		Image:  testImageBase,
// 		Cmd:    []string{"sleep", "60"},
// 		Labels: map[string]string{"test.label": "testvalue"},
// 	}, nil, containerName)
// 	if err != nil {
// 		t.Fatalf("ContainerCreate failed: %v", err)
// 	}
// 	defer testEngine.ContainerRemove(ctx, resp.ID, true)

// 	// Find by extra label
// 	containers, err := testEngine.ContainerListByLabels(ctx, map[string]string{
// 		"test.label": "testvalue",
// 	}, true)
// 	if err != nil {
// 		t.Fatalf("ContainerListByLabels failed: %v", err)
// 	}

// 	found := false
// 	for _, c := range containers {
// 		if c.ID == resp.ID {
// 			found = true
// 		}
// 	}
// 	if !found {
// 		t.Error("ContainerListByLabels should find container with matching label")
// 	}

// 	// Search with non-matching label
// 	containers, err = testEngine.ContainerListByLabels(ctx, map[string]string{
// 		"test.label": "wrongvalue",
// 	}, true)
// 	if err != nil {
// 		t.Fatalf("ContainerListByLabels failed: %v", err)
// 	}

// 	for _, c := range containers {
// 		if c.ID == resp.ID {
// 			t.Error("ContainerListByLabels should not find container with non-matching label")
// 		}
// 	}
// }

// func TestFindManagedContainerByName(t *testing.T) {
// 	ctx := context.Background()

// 	timestamp := time.Now().UnixNano()
// 	managedName := fmt.Sprintf("whail-test-find-managed-%d", timestamp)
// 	unmanagedName := fmt.Sprintf("whail-test-find-unmanaged-%d", timestamp)

// 	// Create managed container
// 	managedResp, err := testEngine.ContainerCreate(ctx, &container.Config{
// 		Image: testImageBase,
// 		Cmd:   []string{"sleep", "60"},
// 	}, nil, managedName)
// 	if err != nil {
// 		t.Fatalf("ContainerCreate failed: %v", err)
// 	}
// 	defer testEngine.ContainerRemove(ctx, managedResp.ID, true)

// 	// Create unmanaged container directly via Docker client
// 	cli := testEngine.Client()
// 	unmanagedResp, err := cli.ContainerCreate(ctx, &container.Config{
// 		Image:  testImageBase,
// 		Cmd:    []string{"sleep", "60"},
// 		Labels: map[string]string{"unmanaged": "true"},
// 	}, nil, nil, nil, unmanagedName)
// 	if err != nil {
// 		t.Fatalf("ContainerCreate failed: %v", err)
// 	}
// 	defer cli.ContainerRemove(ctx, unmanagedResp.ID, container.RemoveOptions{Force: true})

// 	// FindManagedContainerByName should find managed container
// 	found, err := testEngine.FindManagedContainerByName(ctx, managedName)
// 	if err != nil {
// 		t.Fatalf("FindManagedContainerByName failed for managed container: %v", err)
// 	}
// 	if found.ID != managedResp.ID {
// 		t.Errorf("FindManagedContainerByName returned wrong container: got %q, want %q", found.ID, managedResp.ID)
// 	}

// 	// FindManagedContainerByName should NOT find unmanaged container
// 	_, err = testEngine.FindManagedContainerByName(ctx, unmanagedName)
// 	if err == nil {
// 		t.Error("FindManagedContainerByName should not find unmanaged container")
// 	}
// }

// func TestIsContainerManaged(t *testing.T) {
// 	ctx := context.Background()

// 	timestamp := time.Now().UnixNano()
// 	managedName := fmt.Sprintf("whail-test-ismanaged-%d", timestamp)
// 	unmanagedName := fmt.Sprintf("whail-test-isunmanaged-%d", timestamp)

// 	// Create managed container
// 	managedResp, err := testEngine.ContainerCreate(ctx, &container.Config{
// 		Image: testImageBase,
// 		Cmd:   []string{"sleep", "60"},
// 	}, nil, managedName)
// 	if err != nil {
// 		t.Fatalf("ContainerCreate failed: %v", err)
// 	}
// 	defer testEngine.ContainerRemove(ctx, managedResp.ID, true)

// 	// Create unmanaged container
// 	cli := testEngine.Client()
// 	unmanagedResp, err := cli.ContainerCreate(ctx, &container.Config{
// 		Image: testImageBase,
// 		Cmd:   []string{"sleep", "60"},
// 	}, nil, nil, nil, unmanagedName)
// 	if err != nil {
// 		t.Fatalf("ContainerCreate failed: %v", err)
// 	}
// 	defer cli.ContainerRemove(ctx, unmanagedResp.ID, container.RemoveOptions{Force: true})

// 	// Check managed container
// 	isManaged, err := testEngine.IsContainerManaged(ctx, managedResp.ID)
// 	if err != nil {
// 		t.Fatalf("IsContainerManaged failed: %v", err)
// 	}
// 	if !isManaged {
// 		t.Error("IsContainerManaged should return true for managed container")
// 	}

// 	// Check unmanaged container
// 	isManaged, err = testEngine.IsContainerManaged(ctx, unmanagedResp.ID)
// 	if err != nil {
// 		t.Fatalf("IsContainerManaged failed: %v", err)
// 	}
// 	if isManaged {
// 		t.Error("IsContainerManaged should return false for unmanaged container")
// 	}

// 	// Check non-existent container
// 	isManaged, err = testEngine.IsContainerManaged(ctx, "nonexistent-container-id")
// 	if err != nil {
// 		t.Fatalf("IsContainerManaged failed for non-existent: %v", err)
// 	}
// 	if isManaged {
// 		t.Error("IsContainerManaged should return false for non-existent container")
// 	}
// }

// func TestVolumeListByLabels(t *testing.T) {
// 	ctx := context.Background()

// 	timestamp := time.Now().UnixNano()
// 	volumeName := fmt.Sprintf("whail-test-vol-labels-%d", timestamp)

// 	// Create volume with extra label
// 	_, err := testEngine.VolumeCreate(ctx, volumeName, map[string]string{
// 		"test.label": "testvalue",
// 	})
// 	if err != nil {
// 		t.Fatalf("VolumeCreate failed: %v", err)
// 	}
// 	defer testEngine.VolumeRemove(ctx, volumeName, true)

// 	// Find by extra label
// 	volumes, err := testEngine.VolumeListByLabels(ctx, map[string]string{
// 		"test.label": "testvalue",
// 	})
// 	if err != nil {
// 		t.Fatalf("VolumeListByLabels failed: %v", err)
// 	}

// 	found := false
// 	for _, v := range volumes.Volumes {
// 		if v.Name == volumeName {
// 			found = true
// 		}
// 	}
// 	if !found {
// 		t.Error("VolumeListByLabels should find volume with matching label")
// 	}

// 	// Search with non-matching label
// 	volumes, err = testEngine.VolumeListByLabels(ctx, map[string]string{
// 		"test.label": "wrongvalue",
// 	})
// 	if err != nil {
// 		t.Fatalf("VolumeListByLabels failed: %v", err)
// 	}

// 	for _, v := range volumes.Volumes {
// 		if v.Name == volumeName {
// 			t.Error("VolumeListByLabels should not find volume with non-matching label")
// 		}
// 	}
// }

// func TestIsVolumeManaged(t *testing.T) {
// 	ctx := context.Background()

// 	timestamp := time.Now().UnixNano()
// 	managedName := fmt.Sprintf("whail-test-vol-managed-%d", timestamp)
// 	unmanagedName := fmt.Sprintf("whail-test-vol-unmanaged-%d", timestamp)

// 	// Create managed volume
// 	_, err := testEngine.VolumeCreate(ctx, managedName)
// 	if err != nil {
// 		t.Fatalf("VolumeCreate failed: %v", err)
// 	}
// 	defer testEngine.VolumeRemove(ctx, managedName, true)

// 	// Create unmanaged volume
// 	cli := testEngine.Client()
// 	_, err = cli.VolumeCreate(ctx, volume.CreateOptions{
// 		Name:   unmanagedName,
// 		Labels: map[string]string{"unmanaged": "true"},
// 	})
// 	if err != nil {
// 		t.Fatalf("VolumeCreate failed: %v", err)
// 	}
// 	defer cli.VolumeRemove(ctx, unmanagedName, true)

// 	// Check managed volume
// 	isManaged, err := testEngine.IsVolumeManaged(ctx, managedName)
// 	if err != nil {
// 		t.Fatalf("IsVolumeManaged failed: %v", err)
// 	}
// 	if !isManaged {
// 		t.Error("IsVolumeManaged should return true for managed volume")
// 	}

// 	// Check unmanaged volume
// 	isManaged, err = testEngine.IsVolumeManaged(ctx, unmanagedName)
// 	if err != nil {
// 		t.Fatalf("IsVolumeManaged failed: %v", err)
// 	}
// 	if isManaged {
// 		t.Error("IsVolumeManaged should return false for unmanaged volume")
// 	}

// 	// Check non-existent volume
// 	isManaged, err = testEngine.IsVolumeManaged(ctx, "nonexistent-volume")
// 	if err != nil {
// 		t.Fatalf("IsVolumeManaged failed for non-existent: %v", err)
// 	}
// 	if isManaged {
// 		t.Error("IsVolumeManaged should return false for non-existent volume")
// 	}
// }

// func TestIsNetworkManaged(t *testing.T) {
// 	ctx := context.Background()

// 	timestamp := time.Now().UnixNano()
// 	managedName := fmt.Sprintf("whail-test-net-managed-%d", timestamp)
// 	unmanagedName := fmt.Sprintf("whail-test-net-unmanaged-%d", timestamp)

// 	// Create managed network
// 	managedResp, err := testEngine.NetworkCreate(ctx, managedName)
// 	if err != nil {
// 		t.Fatalf("NetworkCreate failed: %v", err)
// 	}
// 	defer testEngine.NetworkRemove(ctx, managedResp.ID)

// 	// Create unmanaged network
// 	cli := testEngine.Client()
// 	unmanagedResp, err := cli.NetworkCreate(ctx, unmanagedName, network.CreateOptions{
// 		Labels: map[string]string{"unmanaged": "true"},
// 	})
// 	if err != nil {
// 		t.Fatalf("NetworkCreate failed: %v", err)
// 	}
// 	defer cli.NetworkRemove(ctx, unmanagedResp.ID)

// 	// Check managed network
// 	isManaged, err := testEngine.IsNetworkManaged(ctx, managedName)
// 	if err != nil {
// 		t.Fatalf("IsNetworkManaged failed: %v", err)
// 	}
// 	if !isManaged {
// 		t.Error("IsNetworkManaged should return true for managed network")
// 	}

// 	// Check unmanaged network
// 	isManaged, err = testEngine.IsNetworkManaged(ctx, unmanagedName)
// 	if err != nil {
// 		t.Fatalf("IsNetworkManaged failed: %v", err)
// 	}
// 	if isManaged {
// 		t.Error("IsNetworkManaged should return false for unmanaged network")
// 	}

// 	// Check non-existent network
// 	isManaged, err = testEngine.IsNetworkManaged(ctx, "nonexistent-network")
// 	if err != nil {
// 		t.Fatalf("IsNetworkManaged failed for non-existent: %v", err)
// 	}
// 	if isManaged {
// 		t.Error("IsNetworkManaged should return false for non-existent network")
// 	}
// }

// func TestImageBuild_AppliesManagedLabels(t *testing.T) {
// 	ctx := context.Background()

// 	imageTag := fmt.Sprintf("whail-test-build:%d", time.Now().UnixNano())
// 	dockerfile := "FROM " + testImageBase + "\nCMD [\"echo\", \"build-test\"]\n"

// 	tarBuf := new(bytes.Buffer)
// 	if err := createTarWithDockerfile(tarBuf, dockerfile); err != nil {
// 		t.Fatalf("createTarWithDockerfile failed: %v", err)
// 	}

// 	// Build using engine (should apply managed labels)
// 	resp, err := testEngine.ImageBuild(ctx, tarBuf, types.ImageBuildOptions{
// 		Tags:       []string{imageTag},
// 		Dockerfile: "Dockerfile",
// 		Remove:     true,
// 	})
// 	if err != nil {
// 		t.Fatalf("ImageBuild failed: %v", err)
// 	}
// 	defer resp.Body.Close()

// 	// Consume the response body
// 	buf := new(bytes.Buffer)
// 	buf.ReadFrom(resp.Body)

// 	// Cleanup
// 	defer testEngine.ImageRemove(ctx, imageTag, true)

// 	// Verify the image has managed labels
// 	isManaged, err := testEngine.IsImageManaged(ctx, imageTag)
// 	if err != nil {
// 		t.Fatalf("IsImageManaged failed: %v", err)
// 	}
// 	if !isManaged {
// 		t.Error("ImageBuild should apply managed labels to built image")
// 	}

// 	// Verify it appears in managed image list
// 	images, err := testEngine.ImageList(ctx)
// 	if err != nil {
// 		t.Fatalf("ImageList failed: %v", err)
// 	}

// 	found := false
// 	for _, img := range images {
// 		for _, tag := range img.RepoTags {
// 			if tag == imageTag {
// 				found = true
// 			}
// 		}
// 	}
// 	if !found {
// 		t.Error("Built image should appear in managed image list")
// 	}
// }
