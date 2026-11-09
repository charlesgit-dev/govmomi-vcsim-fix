// © Broadcom. All Rights Reserved.
// The term "Broadcom" refers to Broadcom Inc. and/or its subsidiaries.
// SPDX-License-Identifier: Apache-2.0

package simulator

import (
	"context"
	"path"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/simulator/esx"
	"github.com/vmware/govmomi/simulator/vpx"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
)

func TestClusterESX(t *testing.T) {
	content := esx.ServiceContent
	s := New(NewServiceInstance(NewContext(), content, esx.RootFolder))

	ts := s.NewServer()
	defer ts.Close()

	ctx := context.Background()
	c, err := govmomi.NewClient(ctx, ts.URL, true)
	if err != nil {
		t.Fatal(err)
	}

	dc := object.NewDatacenter(c.Client, esx.Datacenter.Reference())

	folders, err := dc.Folders(ctx)
	if err != nil {
		t.Fatal(err)
	}

	_, err = folders.HostFolder.CreateCluster(ctx, "cluster1", types.ClusterConfigSpecEx{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestClusterVC(t *testing.T) {
	content := vpx.ServiceContent
	s := New(NewServiceInstance(NewContext(), content, vpx.RootFolder))

	ts := s.NewServer()
	defer ts.Close()

	ctx := context.Background()
	c, err := govmomi.NewClient(ctx, ts.URL, true)
	if err != nil {
		t.Fatal(err)
	}

	f := object.NewRootFolder(c.Client)

	dc, err := f.CreateDatacenter(ctx, "foo")
	if err != nil {
		t.Error(err)
	}

	folders, err := dc.Folders(ctx)
	if err != nil {
		t.Fatal(err)
	}

	cluster, err := folders.HostFolder.CreateCluster(ctx, "cluster1", types.ClusterConfigSpecEx{})
	if err != nil {
		t.Fatal(err)
	}

	// Enable DRS and HA for the cluster
	clusterSpec := types.ClusterConfigSpecEx{
		DrsConfig: &types.ClusterDrsConfigInfo{
			Enabled:           types.NewBool(true),
			DefaultVmBehavior: types.DrsBehaviorFullyAutomated, // Set DRS to fully automated
		},
		DasConfig: &types.ClusterDasConfigInfo{
			Enabled: types.NewBool(true),
		},
	}

	task, err := cluster.Reconfigure(ctx, &clusterSpec, true)
	require.NoError(t, err)
	err = task.Wait(ctx)
	require.NoError(t, err)

	// Check if DRS and HA is set

	finder := find.NewFinder(c.Client, false).SetDatacenter(dc)
	pathname := path.Join(dc.InventoryPath, "host", "cluster1")
	clusterComputeResource, err := finder.ClusterComputeResource(ctx, pathname)
	require.NoError(t, err)
	clusterComputeInfo, err := clusterComputeResource.Configuration(ctx)
	require.NoError(t, err)
	require.NotNil(t, clusterComputeInfo.DrsConfig.Enabled)
	require.NotNil(t, clusterComputeInfo.DasConfig.Enabled)
	require.True(t, *clusterComputeInfo.DrsConfig.Enabled)
	require.True(t, *clusterComputeInfo.DasConfig.Enabled)
	require.True(t, clusterComputeInfo.DrsConfig.DefaultVmBehavior == types.DrsBehaviorFullyAutomated)
	_, err = folders.HostFolder.CreateCluster(ctx, "cluster1", types.ClusterConfigSpecEx{})
	if err == nil {
		t.Error("expected DuplicateName error")
	}

	spec := types.HostConnectSpec{}

	for _, fail := range []bool{true, false} {
		task, err := cluster.AddHost(ctx, spec, true, nil, nil)
		if err != nil {
			t.Fatal(err)
		}

		_, err = task.WaitForResult(ctx, nil)

		if fail {
			if err == nil {
				t.Error("expected error")
			}
			spec.HostName = "localhost"
		} else {
			if err != nil {
				t.Error(err)
			}
		}
	}
}

func TestPlaceVmReconfigure(t *testing.T) {
	tests := []struct {
		name          string
		configSpec    *types.VirtualMachineConfigSpec
		placementType types.PlacementSpecPlacementType
		expectedErr   string
	}{
		{
			"unsupported placement type",
			nil,
			types.PlacementSpecPlacementType("unsupported"),
			"NotSupported",
		},
		{
			"create",
			nil,
			types.PlacementSpecPlacementTypeCreate,
			"",
		},
		{
			"reconfigure with nil config spec",
			nil,
			types.PlacementSpecPlacementTypeReconfigure,
			"InvalidArgument",
		},
		{
			"reconfigure with an empty config spec",
			&types.VirtualMachineConfigSpec{},
			types.PlacementSpecPlacementTypeReconfigure,
			"",
		},
	}

	for _, test := range tests {
		test := test // assign to local var since loop var is reused
		Test(func(ctx context.Context, c *vim25.Client) {
			// Test env setup.
			finder := find.NewFinder(c, true)
			datacenter, err := finder.DefaultDatacenter(ctx)
			if err != nil {
				t.Fatalf("failed to get default datacenter: %v", err)
			}
			finder.SetDatacenter(datacenter)
			vmMoRef := Map(ctx).Any("VirtualMachine").(*VirtualMachine).Reference()
			clusterMoRef := Map(ctx).Any("ClusterComputeResource").(*ClusterComputeResource).Reference()
			clusterObj := object.NewClusterComputeResource(c, clusterMoRef)

			// PlaceVm.
			placementSpec := types.PlacementSpec{
				Vm:            &vmMoRef,
				ConfigSpec:    test.configSpec,
				PlacementType: string(test.placementType),
			}
			_, err = clusterObj.PlaceVm(ctx, placementSpec)
			if err != nil && !strings.Contains(err.Error(), test.expectedErr) {
				t.Fatalf("expected error %q, got %v", test.expectedErr, err)
			}
		})
	}
}

func TestPlaceVmRelocate(t *testing.T) {
	Test(func(ctx context.Context, c *vim25.Client) {
		// Test env setup.
		finder := find.NewFinder(c, true)
		datacenter, err := finder.DefaultDatacenter(ctx)
		if err != nil {
			t.Fatalf("failed to get default datacenter: %v", err)
		}
		finder.SetDatacenter(datacenter)

		vmMoRef := Map(ctx).Any("VirtualMachine").(*VirtualMachine).Reference()
		hostMoRef := Map(ctx).Any("HostSystem").(*HostSystem).Reference()
		dsMoRef := Map(ctx).Any("Datastore").(*Datastore).Reference()

		tests := []struct {
			name         string
			relocateSpec *types.VirtualMachineRelocateSpec
			vmMoRef      *types.ManagedObjectReference
			expectedErr  string
		}{
			{
				"relocate without a spec",
				nil,
				&vmMoRef,
				"",
			},
			{
				"relocate with an empty spec",
				&types.VirtualMachineRelocateSpec{},
				&vmMoRef,
				"",
			},
			{
				"relocate without a vm in spec",
				&types.VirtualMachineRelocateSpec{
					Host: &hostMoRef,
				},
				nil,
				"InvalidArgument",
			},
			{
				"relocate with a non-existing vm in spec",
				&types.VirtualMachineRelocateSpec{
					Host: &hostMoRef,
				},
				&types.ManagedObjectReference{
					Type:  "VirtualMachine",
					Value: "fake-vm-999",
				},
				"InvalidArgument",
			},
			{
				"relocate with a diskId in spec.disk that does not exist in the vm",
				&types.VirtualMachineRelocateSpec{
					Host: &hostMoRef,
					Disk: []types.VirtualMachineRelocateSpecDiskLocator{
						{
							DiskId:    1,
							Datastore: dsMoRef,
						},
					},
				},
				&vmMoRef,
				"InvalidArgument",
			},
			{
				"relocate with a non-existing datastore in spec.disk",
				&types.VirtualMachineRelocateSpec{
					Host: &hostMoRef,
					Disk: []types.VirtualMachineRelocateSpecDiskLocator{
						{
							DiskId: 204, // The default diskId in simulator.
							Datastore: types.ManagedObjectReference{
								Type:  "Datastore",
								Value: "fake-datastore-999",
							},
						},
					},
				},
				&vmMoRef,
				"InvalidArgument",
			},
			{
				"relocate with a valid spec.disk",
				&types.VirtualMachineRelocateSpec{
					Host: &hostMoRef,
					Disk: []types.VirtualMachineRelocateSpecDiskLocator{
						{
							DiskId:    204, // The default diskId in simulator.
							Datastore: dsMoRef,
						},
					},
				},
				&vmMoRef,
				"",
			},
			{
				"relocate with a valid host in spec",
				&types.VirtualMachineRelocateSpec{
					Host: &hostMoRef,
				},
				&vmMoRef,
				"",
			},
			{
				"relocate with a non-existing host in spec",
				&types.VirtualMachineRelocateSpec{
					Host: &types.ManagedObjectReference{
						Type:  "HostSystem",
						Value: "fake-host-999",
					},
				},
				&vmMoRef,
				"ManagedObjectNotFound",
			},
			{
				"relocate with a non-existing datastore in spec",
				&types.VirtualMachineRelocateSpec{
					Datastore: &types.ManagedObjectReference{
						Type:  "Datastore",
						Value: "fake-datastore-999",
					},
				},
				&vmMoRef,
				"ManagedObjectNotFound",
			},
			{
				"relocate with a non-existing resource pool in spec",
				&types.VirtualMachineRelocateSpec{
					Pool: &types.ManagedObjectReference{
						Type:  "ResourcePool",
						Value: "fake-resource-pool-999",
					},
				},
				&vmMoRef,
				"ManagedObjectNotFound",
			},
		}

		for _, test := range tests {
			test := test // assign to local var since loop var is reused
			// PlaceVm.
			placementSpec := types.PlacementSpec{
				Vm:            test.vmMoRef,
				RelocateSpec:  test.relocateSpec,
				PlacementType: string(types.PlacementSpecPlacementTypeRelocate),
			}

			clusterMoRef := Map(ctx).Any("ClusterComputeResource").(*ClusterComputeResource).Reference()
			clusterObj := object.NewClusterComputeResource(c, clusterMoRef)
			_, err = clusterObj.PlaceVm(ctx, placementSpec)
			if err == nil && test.expectedErr != "" {
				t.Fatalf("expected error %q, got nil", test.expectedErr)
			} else if err != nil && !strings.Contains(err.Error(), test.expectedErr) {
				t.Fatalf("expected error %q, got %v", test.expectedErr, err)
			}
		}
	})
}

// TestPlaceVmCreateDatastores verifies PlaceVm honors PlacementSpec.Datastores
// and Hosts for create and clone placement.
func TestPlaceVmCreateDatastores(t *testing.T) {
	model := VPX()
	model.Datastore = 3 // more datastores than hosts, so a wrong index would pick/panic
	model.ClusterHost = 2

	Test(func(ctx context.Context, c *vim25.Client) {
		cluster := Map(ctx).Any("ClusterComputeResource").(*ClusterComputeResource)
		obj := object.NewClusterComputeResource(c, cluster.Reference())

		require.Greater(t, len(cluster.Datastore), 1)
		hosts := cluster.Host

		for _, ptype := range []types.PlacementSpecPlacementType{
			types.PlacementSpecPlacementTypeCreate,
			types.PlacementSpecPlacementTypeClone,
		} {
			// Every datastore in the cluster must be selectable when requested, repeated to defeat randomness.
			for _, ds := range cluster.Datastore {
				for i := 0; i < 10; i++ {
					res, err := obj.PlaceVm(ctx, types.PlacementSpec{
						PlacementType: string(ptype),
						Datastores:    []types.ManagedObjectReference{ds},
						Hosts:         hosts[:1],
					})
					require.NoError(t, err)
					require.NotEmpty(t, res.Recommendations)

					rspec := res.Recommendations[0].Action[0].(*types.PlacementAction).RelocateSpec
					require.NotNil(t, rspec.Datastore)
					require.Equal(t, ds, *rspec.Datastore)
					require.Equal(t, hosts[0], *rspec.Host)
				}
			}

			// Without constraints any cluster datastore is valid.
			res, err := obj.PlaceVm(ctx, types.PlacementSpec{PlacementType: string(ptype)})
			require.NoError(t, err)
			rspec := res.Recommendations[0].Action[0].(*types.PlacementAction).RelocateSpec
			require.Contains(t, cluster.Datastore, *rspec.Datastore)
		}

		// A single-element Datastores list must not index past its length.
		_, err := obj.PlaceVm(ctx, types.PlacementSpec{
			PlacementType: string(types.PlacementSpecPlacementTypeCreate),
			Datastores:    cluster.Datastore[len(cluster.Datastore)-1:],
		})
		require.NoError(t, err)
	}, model)
}

// TestClusterDasConfigDefaults asserts that model-created clusters report an explicit
// HA (DAS) enabled value through the PropertyCollector, rather than an omitted element.
// Clients such as Turbonomic unbox ClusterDasConfigInfo.Enabled and fail if it is absent.
func TestClusterDasConfigDefaults(t *testing.T) {
	m := VPX()
	m.Datacenter = 1
	m.Cluster = 2
	m.ClusterHost = 2
	m.Pool = 2
	m.Machine = 5

	Test(func(ctx context.Context, c *vim25.Client) {
		finder := find.NewFinder(c, false)
		dc, err := finder.DefaultDatacenter(ctx)
		require.NoError(t, err)
		finder.SetDatacenter(dc)

		clusters, err := finder.ClusterComputeResourceList(ctx, "*")
		require.NoError(t, err)
		require.Len(t, clusters, m.Cluster)

		pc := property.DefaultCollector(c)

		for _, cluster := range clusters {
			// configurationEx, via the object helper.
			cfg, err := cluster.Configuration(ctx)
			require.NoError(t, err)
			require.NotNil(t, cfg.DasConfig.Enabled, "%s: dasConfig.enabled is nil", cluster.Name())
			require.False(t, *cfg.DasConfig.Enabled, "HA is not implemented, must default to disabled")
			require.NotNil(t, cfg.DasConfig.AdmissionControlEnabled)
			require.False(t, *cfg.DasConfig.AdmissionControlEnabled)
			require.NotNil(t, cfg.DrsConfig.Enabled)

			// configuration (deprecated ClusterConfigInfo), via the PropertyCollector directly.
			var mc mo.ClusterComputeResource
			err = pc.RetrieveOne(ctx, cluster.Reference(), []string{"configuration", "configurationEx"}, &mc)
			require.NoError(t, err)
			require.NotNil(t, mc.Configuration.DasConfig.Enabled, "configuration.dasConfig.enabled is nil")
			require.False(t, *mc.Configuration.DasConfig.Enabled)
			require.NotNil(t, mc.Configuration.DasConfig.AdmissionControlEnabled)
			require.NotNil(t, mc.Configuration.DrsConfig.Enabled)

			// configuration and configurationEx must agree.
			ex := mc.ConfigurationEx.(*types.ClusterConfigInfoEx)
			require.Equal(t, ex.DasConfig.Enabled, mc.Configuration.DasConfig.Enabled)
			require.Equal(t, ex.DrsConfig.Enabled, mc.Configuration.DrsConfig.Enabled)

			// Cluster still contains hosts, pools and VMs.
			var cr mo.ClusterComputeResource
			err = pc.RetrieveOne(ctx, cluster.Reference(), []string{"host", "resourcePool", "summary"}, &cr)
			require.NoError(t, err)
			require.Len(t, cr.Host, m.ClusterHost)
			require.NotNil(t, cr.ResourcePool)

			pool := object.NewResourcePool(c, *cr.ResourcePool)
			var rp mo.ResourcePool
			require.NoError(t, pc.RetrieveOne(ctx, pool.Reference(), []string{"resourcePool", "vm"}, &rp))
			require.Len(t, rp.ResourcePool, m.Pool)

			vms := 0
			for _, ref := range rp.ResourcePool {
				var child mo.ResourcePool
				require.NoError(t, pc.RetrieveOne(ctx, ref, []string{"vm"}, &child))
				vms += len(child.Vm)
			}
			require.Equal(t, m.Pool*m.Machine, vms)
		}
	}, m)
}

// TestClusterDasConfigReconfigure asserts that ReconfigureComputeResource keeps the
// deprecated "configuration" property in sync with "configurationEx".
func TestClusterDasConfigReconfigure(t *testing.T) {
	Test(func(ctx context.Context, c *vim25.Client) {
		cluster := object.NewClusterComputeResource(c, Map(ctx).Any("ClusterComputeResource").Reference())
		pc := property.DefaultCollector(c)

		for _, enabled := range []bool{true, false} {
			task, err := cluster.Reconfigure(ctx, &types.ClusterConfigSpecEx{
				DasConfig: &types.ClusterDasConfigInfo{Enabled: types.NewBool(enabled)},
			}, true)
			require.NoError(t, err)
			require.NoError(t, task.Wait(ctx))

			var mc mo.ClusterComputeResource
			err = pc.RetrieveOne(ctx, cluster.Reference(), []string{"configuration", "configurationEx"}, &mc)
			require.NoError(t, err)
			require.NotNil(t, mc.Configuration.DasConfig.Enabled)
			require.Equal(t, enabled, *mc.Configuration.DasConfig.Enabled)
			require.Equal(t, enabled, *mc.ConfigurationEx.(*types.ClusterConfigInfoEx).DasConfig.Enabled)
		}
	}, VPX())
}
