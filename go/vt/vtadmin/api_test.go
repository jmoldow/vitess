/*
Copyright 2020 The Vitess Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package vtadmin

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	"vitess.io/vitess/go/vt/grpcclient"
	"vitess.io/vitess/go/vt/topo"
	"vitess.io/vitess/go/vt/topo/memorytopo"
	"vitess.io/vitess/go/vt/topo/topoproto"
	"vitess.io/vitess/go/vt/vitessdriver"
	"vitess.io/vitess/go/vt/vtadmin/cluster"
	"vitess.io/vitess/go/vt/vtadmin/cluster/discovery/fakediscovery"
	"vitess.io/vitess/go/vt/vtadmin/grpcserver"
	"vitess.io/vitess/go/vt/vtadmin/http"
	vtadmintestutil "vitess.io/vitess/go/vt/vtadmin/testutil"
	vtadminvtctldclient "vitess.io/vitess/go/vt/vtadmin/vtctldclient"
	"vitess.io/vitess/go/vt/vtadmin/vtsql"
	"vitess.io/vitess/go/vt/vtadmin/vtsql/fakevtsql"
	"vitess.io/vitess/go/vt/vtctl/grpcvtctldserver"
	"vitess.io/vitess/go/vt/vtctl/grpcvtctldserver/testutil"
	"vitess.io/vitess/go/vt/vtctl/vtctldclient"
	"vitess.io/vitess/go/vt/vttablet/tmclient"

	querypb "vitess.io/vitess/go/vt/proto/query"
	"vitess.io/vitess/go/vt/proto/tabletmanagerdata"
	topodatapb "vitess.io/vitess/go/vt/proto/topodata"
	vtadminpb "vitess.io/vitess/go/vt/proto/vtadmin"
	vtctldatapb "vitess.io/vitess/go/vt/proto/vtctldata"
	"vitess.io/vitess/go/vt/proto/vttime"
)

func init() {
	*tmclient.TabletManagerProtocol = testutil.TabletManagerClientProtocol
}

func TestGetClusters(t *testing.T) {
	tests := []struct {
		name     string
		clusters []*cluster.Cluster
		expected []*vtadminpb.Cluster
	}{
		{
			name: "multiple clusters",
			clusters: []*cluster.Cluster{
				{
					ID:        "c1",
					Name:      "cluster1",
					Discovery: fakediscovery.New(),
				},
				{
					ID:        "c2",
					Name:      "cluster2",
					Discovery: fakediscovery.New(),
				},
			},
			expected: []*vtadminpb.Cluster{
				{
					Id:   "c1",
					Name: "cluster1",
				},
				{
					Id:   "c2",
					Name: "cluster2",
				},
			},
		},
		{
			name:     "no clusters",
			clusters: []*cluster.Cluster{},
			expected: []*vtadminpb.Cluster{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := NewAPI(tt.clusters, grpcserver.Options{}, http.Options{})
			ctx := context.Background()

			resp, err := api.GetClusters(ctx, &vtadminpb.GetClustersRequest{})
			assert.NoError(t, err)
			assert.ElementsMatch(t, tt.expected, resp.Clusters)
		})
	}
}

func TestGetGates(t *testing.T) {
	fakedisco1 := fakediscovery.New()
	cluster1 := &cluster.Cluster{
		ID:        "c1",
		Name:      "cluster1",
		Discovery: fakedisco1,
	}
	cluster1Gates := []*vtadminpb.VTGate{
		{
			Hostname: "cluster1-gate1",
		},
		{
			Hostname: "cluster1-gate2",
		},
		{
			Hostname: "cluster1-gate3",
		},
	}
	fakedisco1.AddTaggedGates(nil, cluster1Gates...)

	expectedCluster1Gates := []*vtadminpb.VTGate{
		{
			Cluster: &vtadminpb.Cluster{
				Id:   cluster1.ID,
				Name: cluster1.Name,
			},
			Hostname: "cluster1-gate1",
		},
		{
			Cluster: &vtadminpb.Cluster{
				Id:   cluster1.ID,
				Name: cluster1.Name,
			},
			Hostname: "cluster1-gate2",
		},
		{
			Cluster: &vtadminpb.Cluster{
				Id:   cluster1.ID,
				Name: cluster1.Name,
			},
			Hostname: "cluster1-gate3",
		},
	}

	fakedisco2 := fakediscovery.New()
	cluster2 := &cluster.Cluster{
		ID:        "c2",
		Name:      "cluster2",
		Discovery: fakedisco2,
	}
	cluster2Gates := []*vtadminpb.VTGate{
		{
			Hostname: "cluster2-gate1",
		},
	}
	fakedisco2.AddTaggedGates(nil, cluster2Gates...)

	expectedCluster2Gates := []*vtadminpb.VTGate{
		{
			Cluster: &vtadminpb.Cluster{
				Id:   cluster2.ID,
				Name: cluster2.Name,
			},
			Hostname: "cluster2-gate1",
		},
	}

	api := NewAPI([]*cluster.Cluster{cluster1, cluster2}, grpcserver.Options{}, http.Options{})
	ctx := context.Background()

	resp, err := api.GetGates(ctx, &vtadminpb.GetGatesRequest{})
	assert.NoError(t, err)
	assert.ElementsMatch(t, append(expectedCluster1Gates, expectedCluster2Gates...), resp.Gates)

	resp, err = api.GetGates(ctx, &vtadminpb.GetGatesRequest{ClusterIds: []string{cluster1.ID}})
	assert.NoError(t, err)
	assert.ElementsMatch(t, expectedCluster1Gates, resp.Gates)

	fakedisco1.SetGatesError(true)

	resp, err = api.GetGates(ctx, &vtadminpb.GetGatesRequest{})
	assert.Error(t, err)
	assert.Nil(t, resp)
}

func TestGetKeyspaces(t *testing.T) {
	ts1 := memorytopo.NewServer("c1_cell1")
	ts2 := memorytopo.NewServer("c2_cell1")

	testutil.AddKeyspace(context.Background(), t, ts1, &vtctldatapb.Keyspace{
		Name:     "testkeyspace",
		Keyspace: &topodatapb.Keyspace{},
	})
	testutil.AddKeyspace(context.Background(), t, ts1, &vtctldatapb.Keyspace{
		Name: "snapshot",
		Keyspace: &topodatapb.Keyspace{
			KeyspaceType: topodatapb.KeyspaceType_SNAPSHOT,
			BaseKeyspace: "testkeyspace",
			SnapshotTime: &vttime.Time{Seconds: 10, Nanoseconds: 1},
		},
	})

	testutil.AddKeyspace(context.Background(), t, ts2, &vtctldatapb.Keyspace{
		Name:     "customer",
		Keyspace: &topodatapb.Keyspace{},
	})

	testutil.WithTestServer(t, grpcvtctldserver.NewVtctldServer(ts1), func(t *testing.T, cluster1Client vtctldclient.VtctldClient) {
		testutil.WithTestServer(t, grpcvtctldserver.NewVtctldServer(ts2), func(t *testing.T, cluster2Client vtctldclient.VtctldClient) {
			c1 := buildCluster(1, cluster1Client, nil, nil)
			c2 := buildCluster(2, cluster2Client, nil, nil)

			api := NewAPI([]*cluster.Cluster{c1, c2}, grpcserver.Options{}, http.Options{})
			resp, err := api.GetKeyspaces(context.Background(), &vtadminpb.GetKeyspacesRequest{})
			require.NoError(t, err)

			expected := &vtadminpb.GetKeyspacesResponse{
				Keyspaces: []*vtadminpb.Keyspace{
					{
						Cluster: &vtadminpb.Cluster{
							Id:   "c1",
							Name: "cluster1",
						},
						Keyspace: &vtctldatapb.Keyspace{
							Name:     "testkeyspace",
							Keyspace: &topodatapb.Keyspace{},
						},
					},
					{
						Cluster: &vtadminpb.Cluster{
							Id:   "c1",
							Name: "cluster1",
						},
						Keyspace: &vtctldatapb.Keyspace{
							Name: "snapshot",
							Keyspace: &topodatapb.Keyspace{
								KeyspaceType: topodatapb.KeyspaceType_SNAPSHOT,
								BaseKeyspace: "testkeyspace",
								SnapshotTime: &vttime.Time{Seconds: 10, Nanoseconds: 1},
							},
						},
					},
					{
						Cluster: &vtadminpb.Cluster{
							Id:   "c2",
							Name: "cluster2",
						},
						Keyspace: &vtctldatapb.Keyspace{
							Name:     "customer",
							Keyspace: &topodatapb.Keyspace{},
						},
					},
				},
			}
			assert.ElementsMatch(t, expected.Keyspaces, resp.Keyspaces)

			resp, err = api.GetKeyspaces(
				context.Background(),
				&vtadminpb.GetKeyspacesRequest{
					ClusterIds: []string{"c1"},
				},
			)
			require.NoError(t, err)

			expected.Keyspaces = expected.Keyspaces[:2] // just c1
			assert.ElementsMatch(t, expected.Keyspaces, resp.Keyspaces)
		})
	})
}

func TestGetSchemas(t *testing.T) {
	tests := []struct {
		name           string
		clusterTablets [][]*vtadminpb.Tablet
		// Indexed by tablet alias
		tabletSchemas map[string]*tabletmanagerdata.SchemaDefinition
		req           *vtadminpb.GetSchemasRequest
		expected      *vtadminpb.GetSchemasResponse
	}{
		{
			name: "one schema in one cluster",
			clusterTablets: [][]*vtadminpb.Tablet{
				// cluster0
				{
					{
						State: vtadminpb.Tablet_SERVING,
						Tablet: &topodatapb.Tablet{
							Alias: &topodatapb.TabletAlias{
								Cell: "c0_cell1",
								Uid:  100,
							},
							Keyspace: "commerce",
						},
					},
				},
				// cluster1
				{
					{
						State: vtadminpb.Tablet_SERVING,
						Tablet: &topodatapb.Tablet{
							Alias: &topodatapb.TabletAlias{
								Cell: "c1_cell1",
								Uid:  100,
							},
							Keyspace: "commerce",
						},
					},
				},
			},
			tabletSchemas: map[string]*tabletmanagerdata.SchemaDefinition{
				"c0_cell1-0000000100": {
					DatabaseSchema: "CREATE DATABASE vt_testkeyspace",
					TableDefinitions: []*tabletmanagerdata.TableDefinition{
						{
							Name:       "t1",
							Schema:     `CREATE TABLE t1 (id int(11) not null,PRIMARY KEY (id));`,
							Type:       "BASE",
							Columns:    []string{"id"},
							DataLength: 100,
							RowCount:   50,
							Fields: []*querypb.Field{
								{
									Name: "id",
									Type: querypb.Type_INT32,
								},
							},
						},
					},
				},
			},
			req: &vtadminpb.GetSchemasRequest{},
			expected: &vtadminpb.GetSchemasResponse{
				Schemas: []*vtadminpb.Schema{
					{
						Cluster: &vtadminpb.Cluster{
							Id:   "c0",
							Name: "cluster0",
						},
						Keyspace: "commerce",
						TableDefinitions: []*tabletmanagerdata.TableDefinition{
							{
								Name:       "t1",
								Schema:     `CREATE TABLE t1 (id int(11) not null,PRIMARY KEY (id));`,
								Type:       "BASE",
								Columns:    []string{"id"},
								DataLength: 100,
								RowCount:   50,
								Fields: []*querypb.Field{
									{
										Name: "id",
										Type: querypb.Type_INT32,
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "one schema in each cluster",
			clusterTablets: [][]*vtadminpb.Tablet{
				// cluster0
				{
					{
						State: vtadminpb.Tablet_SERVING,
						Tablet: &topodatapb.Tablet{
							Alias: &topodatapb.TabletAlias{
								Cell: "c0_cell1",
								Uid:  100,
							},
							Keyspace: "commerce",
						},
					},
				},
				// cluster1
				{
					{
						State: vtadminpb.Tablet_SERVING,
						Tablet: &topodatapb.Tablet{
							Alias: &topodatapb.TabletAlias{
								Cell: "c1_cell1",
								Uid:  100,
							},
							Keyspace: "commerce",
						},
					},
				},
			},
			tabletSchemas: map[string]*tabletmanagerdata.SchemaDefinition{
				"c0_cell1-0000000100": {
					DatabaseSchema: "CREATE DATABASE vt_testkeyspace",
					TableDefinitions: []*tabletmanagerdata.TableDefinition{
						{
							Name:       "t1",
							Schema:     `CREATE TABLE t1 (id int(11) not null,PRIMARY KEY (id));`,
							Type:       "BASE",
							Columns:    []string{"id"},
							DataLength: 100,
							RowCount:   50,
							Fields: []*querypb.Field{
								{
									Name: "id",
									Type: querypb.Type_INT32,
								},
							},
						},
					},
				},
				"c1_cell1-0000000100": {
					DatabaseSchema: "CREATE DATABASE vt_testkeyspace",
					TableDefinitions: []*tabletmanagerdata.TableDefinition{
						{
							Name:       "t2",
							Schema:     `CREATE TABLE t2 (id int(11) not null,PRIMARY KEY (id));`,
							Type:       "BASE",
							Columns:    []string{"id"},
							DataLength: 100,
							RowCount:   50,
							Fields: []*querypb.Field{
								{
									Name: "id",
									Type: querypb.Type_INT32,
								},
							},
						},
					},
				},
			},
			req: &vtadminpb.GetSchemasRequest{},
			expected: &vtadminpb.GetSchemasResponse{
				Schemas: []*vtadminpb.Schema{
					{
						Cluster: &vtadminpb.Cluster{
							Id:   "c0",
							Name: "cluster0",
						},
						Keyspace: "commerce",
						TableDefinitions: []*tabletmanagerdata.TableDefinition{
							{
								Name:       "t1",
								Schema:     `CREATE TABLE t1 (id int(11) not null,PRIMARY KEY (id));`,
								Type:       "BASE",
								Columns:    []string{"id"},
								DataLength: 100,
								RowCount:   50,
								Fields: []*querypb.Field{
									{
										Name: "id",
										Type: querypb.Type_INT32,
									},
								},
							},
						},
					},
					{
						Cluster: &vtadminpb.Cluster{
							Id:   "c1",
							Name: "cluster1",
						},
						Keyspace: "commerce",
						TableDefinitions: []*tabletmanagerdata.TableDefinition{
							{
								Name:       "t2",
								Schema:     `CREATE TABLE t2 (id int(11) not null,PRIMARY KEY (id));`,
								Type:       "BASE",
								Columns:    []string{"id"},
								DataLength: 100,
								RowCount:   50,
								Fields: []*querypb.Field{
									{
										Name: "id",
										Type: querypb.Type_INT32,
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "filtered by cluster ID",
			clusterTablets: [][]*vtadminpb.Tablet{
				// cluster0
				{
					{
						State: vtadminpb.Tablet_SERVING,
						Tablet: &topodatapb.Tablet{
							Alias: &topodatapb.TabletAlias{
								Cell: "c0_cell1",
								Uid:  100,
							},
							Keyspace: "commerce",
						},
					},
				},
				// cluster1
				{
					{
						State: vtadminpb.Tablet_SERVING,
						Tablet: &topodatapb.Tablet{
							Alias: &topodatapb.TabletAlias{
								Cell: "c1_cell1",
								Uid:  100,
							},
							Keyspace: "commerce",
						},
					},
				},
			},
			tabletSchemas: map[string]*tabletmanagerdata.SchemaDefinition{
				"c0_cell1-0000000100": {
					DatabaseSchema: "CREATE DATABASE vt_testkeyspace",
					TableDefinitions: []*tabletmanagerdata.TableDefinition{
						{
							Name:       "t1",
							Schema:     `CREATE TABLE t1 (id int(11) not null,PRIMARY KEY (id));`,
							Type:       "BASE",
							Columns:    []string{"id"},
							DataLength: 100,
							RowCount:   50,
							Fields: []*querypb.Field{
								{
									Name: "id",
									Type: querypb.Type_INT32,
								},
							},
						},
					},
				},
				"c1_cell1-0000000100": {
					DatabaseSchema: "CREATE DATABASE vt_testkeyspace",
					TableDefinitions: []*tabletmanagerdata.TableDefinition{
						{
							Name:       "t2",
							Schema:     `CREATE TABLE t2 (id int(11) not null,PRIMARY KEY (id));`,
							Type:       "BASE",
							Columns:    []string{"id"},
							DataLength: 100,
							RowCount:   50,
							Fields: []*querypb.Field{
								{
									Name: "id",
									Type: querypb.Type_INT32,
								},
							},
						},
					},
				},
			},
			req: &vtadminpb.GetSchemasRequest{
				ClusterIds: []string{"c1"},
			},
			expected: &vtadminpb.GetSchemasResponse{
				Schemas: []*vtadminpb.Schema{
					{
						Cluster: &vtadminpb.Cluster{
							Id:   "c1",
							Name: "cluster1",
						},
						Keyspace: "commerce",
						TableDefinitions: []*tabletmanagerdata.TableDefinition{
							{
								Name:       "t2",
								Schema:     `CREATE TABLE t2 (id int(11) not null,PRIMARY KEY (id));`,
								Type:       "BASE",
								Columns:    []string{"id"},
								DataLength: 100,
								RowCount:   50,
								Fields: []*querypb.Field{
									{
										Name: "id",
										Type: querypb.Type_INT32,
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "filtered by cluster ID that doesn't exist",
			clusterTablets: [][]*vtadminpb.Tablet{
				// cluster0
				{
					{
						State: vtadminpb.Tablet_SERVING,
						Tablet: &topodatapb.Tablet{
							Alias: &topodatapb.TabletAlias{
								Cell: "c0_cell1",
								Uid:  100,
							},
							Keyspace: "commerce",
						},
					},
				},
			},
			tabletSchemas: map[string]*tabletmanagerdata.SchemaDefinition{
				"c0_cell1-0000000100": {
					DatabaseSchema: "CREATE DATABASE vt_testkeyspace",
					TableDefinitions: []*tabletmanagerdata.TableDefinition{
						{
							Name:       "t1",
							Schema:     `CREATE TABLE t1 (id int(11) not null,PRIMARY KEY (id));`,
							Type:       "BASE",
							Columns:    []string{"id"},
							DataLength: 100,
							RowCount:   50,
							Fields: []*querypb.Field{
								{
									Name: "id",
									Type: querypb.Type_INT32,
								},
							},
						},
					},
				},
			},
			req: &vtadminpb.GetSchemasRequest{
				ClusterIds: []string{"nope"},
			},
			expected: &vtadminpb.GetSchemasResponse{
				Schemas: []*vtadminpb.Schema{},
			},
		},
		{
			name: "no schemas for any cluster",
			clusterTablets: [][]*vtadminpb.Tablet{
				// cluster0
				{
					{
						State: vtadminpb.Tablet_SERVING,
						Tablet: &topodatapb.Tablet{
							Alias: &topodatapb.TabletAlias{
								Cell: "c0_cell1",
								Uid:  100,
							},
							Keyspace: "commerce",
						},
					},
				},
			},
			tabletSchemas: map[string]*tabletmanagerdata.SchemaDefinition{},
			req:           &vtadminpb.GetSchemasRequest{},
			expected: &vtadminpb.GetSchemasResponse{
				Schemas: []*vtadminpb.Schema{},
			},
		},
		{
			name: "no serving tablets",
			clusterTablets: [][]*vtadminpb.Tablet{
				// cluster0
				{
					{
						State: vtadminpb.Tablet_NOT_SERVING,
						Tablet: &topodatapb.Tablet{
							Alias: &topodatapb.TabletAlias{
								Cell: "c0_cell1",
								Uid:  100,
							},
							Keyspace: "commerce",
						},
					},
				},
			},
			tabletSchemas: map[string]*tabletmanagerdata.SchemaDefinition{
				"c0_cell1-0000000100": {
					DatabaseSchema: "CREATE DATABASE vt_testkeyspace",
					TableDefinitions: []*tabletmanagerdata.TableDefinition{
						{
							Name:       "t1",
							Schema:     `CREATE TABLE t1 (id int(11) not null,PRIMARY KEY (id));`,
							Type:       "BASE",
							Columns:    []string{"id"},
							DataLength: 100,
							RowCount:   50,
							Fields: []*querypb.Field{
								{
									Name: "id",
									Type: querypb.Type_INT32,
								},
							},
						},
					},
				},
			},
			req: &vtadminpb.GetSchemasRequest{},
			expected: &vtadminpb.GetSchemasResponse{
				Schemas: []*vtadminpb.Schema{},
			},
		},
	}

	for _, tt := range tests {
		testutil.TabletManagerClient.Schemas = map[string]*tabletmanagerdata.SchemaDefinition{}

		topos := []*topo.Server{
			memorytopo.NewServer("c0_cell1"),
			memorytopo.NewServer("c1_cell1"),
		}

		// Setting up WithTestServer in a generic, recursive way is... unpleasant,
		// so all tests are set-up and run in the context of these two clusters.
		testutil.WithTestServer(t, grpcvtctldserver.NewVtctldServer(topos[0]), func(t *testing.T, cluster0Client vtctldclient.VtctldClient) {
			testutil.WithTestServer(t, grpcvtctldserver.NewVtctldServer(topos[1]), func(t *testing.T, cluster1Client vtctldclient.VtctldClient) {
				// Put 'em in a slice so we can look them up by index
				clusterClients := []vtctldclient.VtctldClient{cluster0Client, cluster1Client}

				// Build the clusters
				clusters := make([]*cluster.Cluster, len(topos))
				for cdx, toposerver := range topos {
					// Handle when a test doesn't define any tablets for a given cluster.
					var cts []*vtadminpb.Tablet
					if cdx < len(tt.clusterTablets) {
						cts = tt.clusterTablets[cdx]
					}

					for _, tablet := range cts {
						// AddTablet also adds the keyspace + shard for us.
						testutil.AddTablet(context.Background(), t, toposerver, tablet.Tablet)

						// Adds each SchemaDefinition to the fake TabletManagerClient, or nil
						// if there are no schemas for that tablet. (All tablet aliases must
						// exist in the map. Otherwise, TabletManagerClient will return an error when
						// looking up the schema with tablet alias that doesn't exist.)
						alias := topoproto.TabletAliasString(tablet.Tablet.Alias)
						testutil.TabletManagerClient.Schemas[alias] = tt.tabletSchemas[alias]
					}

					clusters[cdx] = buildCluster(cdx, clusterClients[cdx], cts, nil)
				}

				api := NewAPI(clusters, grpcserver.Options{}, http.Options{})

				resp, err := api.GetSchemas(context.Background(), tt.req)
				require.NoError(t, err)

				vtadmintestutil.AssertSchemaSlicesEqual(t, tt.expected.Schemas, resp.Schemas, tt.name)
			})
		})
	}
}

func TestGetTablets(t *testing.T) {
	tests := []struct {
		name           string
		clusterTablets [][]*vtadminpb.Tablet
		dbconfigs      map[string]*dbcfg
		req            *vtadminpb.GetTabletsRequest
		expected       []*vtadminpb.Tablet
		shouldErr      bool
	}{
		{
			name: "single cluster",
			clusterTablets: [][]*vtadminpb.Tablet{
				{
					/* cluster 0 */
					{
						State: vtadminpb.Tablet_SERVING,
						Tablet: &topodatapb.Tablet{
							Alias: &topodatapb.TabletAlias{
								Uid:  100,
								Cell: "zone1",
							},
							Hostname: "ks1-00-00-zone1-a",
							Keyspace: "ks1",
							Shard:    "-",
							Type:     topodatapb.TabletType_MASTER,
						},
					},
				},
			},
			dbconfigs: map[string]*dbcfg{},
			req:       &vtadminpb.GetTabletsRequest{},
			expected: []*vtadminpb.Tablet{
				{
					Cluster: &vtadminpb.Cluster{
						Id:   "c0",
						Name: "cluster0",
					},
					State: vtadminpb.Tablet_SERVING,
					Tablet: &topodatapb.Tablet{
						Alias: &topodatapb.TabletAlias{
							Uid:  100,
							Cell: "zone1",
						},
						Hostname: "ks1-00-00-zone1-a",
						Keyspace: "ks1",
						Shard:    "-",
						Type:     topodatapb.TabletType_MASTER,
					},
				},
			},
			shouldErr: false,
		},
		{
			name: "one cluster errors",
			clusterTablets: [][]*vtadminpb.Tablet{
				/* cluster 0 */
				{
					{
						State: vtadminpb.Tablet_SERVING,
						Tablet: &topodatapb.Tablet{
							Alias: &topodatapb.TabletAlias{
								Uid:  100,
								Cell: "zone1",
							},
							Hostname: "ks1-00-00-zone1-a",
							Keyspace: "ks1",
							Shard:    "-",
							Type:     topodatapb.TabletType_MASTER,
						},
					},
				},
				/* cluster 1 */
				{
					{
						State: vtadminpb.Tablet_SERVING,
						Tablet: &topodatapb.Tablet{
							Alias: &topodatapb.TabletAlias{
								Uid:  200,
								Cell: "zone1",
							},
							Hostname: "ks2-00-00-zone1-a",
							Keyspace: "ks2",
							Shard:    "-",
							Type:     topodatapb.TabletType_MASTER,
						},
					},
				},
			},
			dbconfigs: map[string]*dbcfg{
				"c1": {shouldErr: true},
			},
			req:       &vtadminpb.GetTabletsRequest{},
			expected:  nil,
			shouldErr: true,
		},
		{
			name: "multi cluster, selecting one",
			clusterTablets: [][]*vtadminpb.Tablet{
				/* cluster 0 */
				{
					{
						State: vtadminpb.Tablet_SERVING,
						Tablet: &topodatapb.Tablet{
							Alias: &topodatapb.TabletAlias{
								Uid:  100,
								Cell: "zone1",
							},
							Hostname: "ks1-00-00-zone1-a",
							Keyspace: "ks1",
							Shard:    "-",
							Type:     topodatapb.TabletType_MASTER,
						},
					},
				},
				/* cluster 1 */
				{
					{
						State: vtadminpb.Tablet_SERVING,
						Tablet: &topodatapb.Tablet{
							Alias: &topodatapb.TabletAlias{
								Uid:  200,
								Cell: "zone1",
							},
							Hostname: "ks2-00-00-zone1-a",
							Keyspace: "ks2",
							Shard:    "-",
							Type:     topodatapb.TabletType_MASTER,
						},
					},
				},
			},
			dbconfigs: map[string]*dbcfg{},
			req:       &vtadminpb.GetTabletsRequest{ClusterIds: []string{"c0"}},
			expected: []*vtadminpb.Tablet{
				{
					Cluster: &vtadminpb.Cluster{
						Id:   "c0",
						Name: "cluster0",
					},
					State: vtadminpb.Tablet_SERVING,
					Tablet: &topodatapb.Tablet{
						Alias: &topodatapb.TabletAlias{
							Uid:  100,
							Cell: "zone1",
						},
						Hostname: "ks1-00-00-zone1-a",
						Keyspace: "ks1",
						Shard:    "-",
						Type:     topodatapb.TabletType_MASTER,
					},
				},
			},
			shouldErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clusters := make([]*cluster.Cluster, len(tt.clusterTablets))

			for i, tablets := range tt.clusterTablets {
				cluster := buildCluster(i, nil, tablets, tt.dbconfigs)
				clusters[i] = cluster
			}

			api := NewAPI(clusters, grpcserver.Options{}, http.Options{})
			resp, err := api.GetTablets(context.Background(), tt.req)
			if tt.shouldErr {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)
			assert.ElementsMatch(t, tt.expected, resp.Tablets)
		})
	}
}

// This test only validates the error handling on dialing database connections.
// Other cases are covered by one or both of TestGetTablets and TestGetTablet.
func Test_getTablets(t *testing.T) {
	api := &API{}
	disco := fakediscovery.New()
	disco.AddTaggedGates(nil, &vtadminpb.VTGate{Hostname: "gate"})

	db := vtsql.New(&vtsql.Config{
		Cluster: &vtadminpb.Cluster{
			Id:   "c1",
			Name: "one",
		},
		Discovery: disco,
	})
	db.DialFunc = func(cfg vitessdriver.Configuration) (*sql.DB, error) {
		return nil, assert.AnError
	}

	_, err := api.getTablets(context.Background(), &cluster.Cluster{
		DB: db,
	})
	assert.Error(t, err)
}

func TestGetTablet(t *testing.T) {
	tests := []struct {
		name           string
		clusterTablets [][]*vtadminpb.Tablet
		dbconfigs      map[string]*dbcfg
		req            *vtadminpb.GetTabletRequest
		expected       *vtadminpb.Tablet
		shouldErr      bool
	}{
		{
			name: "single cluster",
			clusterTablets: [][]*vtadminpb.Tablet{
				{
					/* cluster 0 */
					{
						State: vtadminpb.Tablet_SERVING,
						Tablet: &topodatapb.Tablet{
							Alias: &topodatapb.TabletAlias{
								Uid:  100,
								Cell: "zone1",
							},
							Hostname: "ks1-00-00-zone1-a",
							Keyspace: "ks1",
							Shard:    "-",
							Type:     topodatapb.TabletType_MASTER,
						},
					},
				},
			},
			dbconfigs: map[string]*dbcfg{},
			req: &vtadminpb.GetTabletRequest{
				Hostname: "ks1-00-00-zone1-a",
			},
			expected: &vtadminpb.Tablet{
				Cluster: &vtadminpb.Cluster{
					Id:   "c0",
					Name: "cluster0",
				},
				State: vtadminpb.Tablet_SERVING,
				Tablet: &topodatapb.Tablet{
					Alias: &topodatapb.TabletAlias{
						Uid:  100,
						Cell: "zone1",
					},
					Hostname: "ks1-00-00-zone1-a",
					Keyspace: "ks1",
					Shard:    "-",
					Type:     topodatapb.TabletType_MASTER,
				},
			},
			shouldErr: false,
		},
		{
			name: "one cluster errors",
			clusterTablets: [][]*vtadminpb.Tablet{
				/* cluster 0 */
				{
					{
						State: vtadminpb.Tablet_SERVING,
						Tablet: &topodatapb.Tablet{
							Alias: &topodatapb.TabletAlias{
								Uid:  100,
								Cell: "zone1",
							},
							Hostname: "ks1-00-00-zone1-a",
							Keyspace: "ks1",
							Shard:    "-",
							Type:     topodatapb.TabletType_MASTER,
						},
					},
				},
				/* cluster 1 */
				{
					{
						State: vtadminpb.Tablet_SERVING,
						Tablet: &topodatapb.Tablet{
							Alias: &topodatapb.TabletAlias{
								Uid:  200,
								Cell: "zone1",
							},
							Hostname: "ks2-00-00-zone1-a",
							Keyspace: "ks2",
							Shard:    "-",
							Type:     topodatapb.TabletType_MASTER,
						},
					},
				},
			},
			dbconfigs: map[string]*dbcfg{
				"c1": {shouldErr: true},
			},
			req: &vtadminpb.GetTabletRequest{
				Hostname: "doesn't matter",
			},
			expected:  nil,
			shouldErr: true,
		},
		{
			name: "multi cluster, selecting one with tablet",
			clusterTablets: [][]*vtadminpb.Tablet{
				/* cluster 0 */
				{
					{
						State: vtadminpb.Tablet_SERVING,
						Tablet: &topodatapb.Tablet{
							Alias: &topodatapb.TabletAlias{
								Uid:  100,
								Cell: "zone1",
							},
							Hostname: "ks1-00-00-zone1-a",
							Keyspace: "ks1",
							Shard:    "-",
							Type:     topodatapb.TabletType_MASTER,
						},
					},
				},
				/* cluster 1 */
				{
					{
						State: vtadminpb.Tablet_SERVING,
						Tablet: &topodatapb.Tablet{
							Alias: &topodatapb.TabletAlias{
								Uid:  200,
								Cell: "zone1",
							},
							Hostname: "ks2-00-00-zone1-a",
							Keyspace: "ks2",
							Shard:    "-",
							Type:     topodatapb.TabletType_MASTER,
						},
					},
				},
			},
			dbconfigs: map[string]*dbcfg{},
			req: &vtadminpb.GetTabletRequest{
				Hostname:   "ks1-00-00-zone1-a",
				ClusterIds: []string{"c0"},
			},
			expected: &vtadminpb.Tablet{
				Cluster: &vtadminpb.Cluster{
					Id:   "c0",
					Name: "cluster0",
				},
				State: vtadminpb.Tablet_SERVING,
				Tablet: &topodatapb.Tablet{
					Alias: &topodatapb.TabletAlias{
						Uid:  100,
						Cell: "zone1",
					},
					Hostname: "ks1-00-00-zone1-a",
					Keyspace: "ks1",
					Shard:    "-",
					Type:     topodatapb.TabletType_MASTER,
				},
			},
			shouldErr: false,
		},
		{
			name: "multi cluster, multiple results",
			clusterTablets: [][]*vtadminpb.Tablet{
				/* cluster 0 */
				{
					{
						State: vtadminpb.Tablet_SERVING,
						Tablet: &topodatapb.Tablet{
							Alias: &topodatapb.TabletAlias{
								Uid:  100,
								Cell: "zone1",
							},
							Hostname: "ks1-00-00-zone1-a",
							Keyspace: "ks1",
							Shard:    "-",
							Type:     topodatapb.TabletType_MASTER,
						},
					},
				},
				/* cluster 1 */
				{
					{
						State: vtadminpb.Tablet_SERVING,
						Tablet: &topodatapb.Tablet{
							Alias: &topodatapb.TabletAlias{
								Uid:  200,
								Cell: "zone1",
							},
							Hostname: "ks1-00-00-zone1-a",
							Keyspace: "ks1",
							Shard:    "-",
							Type:     topodatapb.TabletType_MASTER,
						},
					},
				},
			},
			dbconfigs: map[string]*dbcfg{},
			req: &vtadminpb.GetTabletRequest{
				Hostname: "ks1-00-00-zone1-a",
			},
			expected:  nil,
			shouldErr: true,
		},
		{
			name: "no results",
			clusterTablets: [][]*vtadminpb.Tablet{
				/* cluster 0 */
				{},
			},
			dbconfigs: map[string]*dbcfg{},
			req: &vtadminpb.GetTabletRequest{
				Hostname: "ks1-00-00-zone1-a",
			},
			expected:  nil,
			shouldErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clusters := make([]*cluster.Cluster, len(tt.clusterTablets))

			for i, tablets := range tt.clusterTablets {
				cluster := buildCluster(i, nil, tablets, tt.dbconfigs)
				clusters[i] = cluster
			}

			api := NewAPI(clusters, grpcserver.Options{}, http.Options{})
			resp, err := api.GetTablet(context.Background(), tt.req)
			if tt.shouldErr {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)
			assert.Equal(t, tt.expected, resp)
		})
	}
}

type dbcfg struct {
	shouldErr bool
}

// shared helper for building a cluster that contains the given tablets and
// talking to the given vtctld server. dbconfigs contains an optional config
// for controlling the behavior of the cluster's DB at the package sql level.
func buildCluster(i int, vtctldClient vtctldclient.VtctldClient, tablets []*vtadminpb.Tablet, dbconfigs map[string]*dbcfg) *cluster.Cluster {
	disco := fakediscovery.New()
	disco.AddTaggedGates(nil, &vtadminpb.VTGate{Hostname: fmt.Sprintf("cluster%d-gate", i)})
	disco.AddTaggedVtctlds(nil, &vtadminpb.Vtctld{Hostname: "doesn't matter"})

	cluster := &cluster.Cluster{
		ID:        fmt.Sprintf("c%d", i),
		Name:      fmt.Sprintf("cluster%d", i),
		Discovery: disco,
	}

	dbconfig, ok := dbconfigs[cluster.ID]
	if !ok {
		dbconfig = &dbcfg{shouldErr: false}
	}

	db := vtsql.New(&vtsql.Config{
		Cluster:   cluster.ToProto(),
		Discovery: disco,
	})
	db.DialFunc = func(cfg vitessdriver.Configuration) (*sql.DB, error) {
		return sql.OpenDB(&fakevtsql.Connector{Tablets: tablets, ShouldErr: dbconfig.shouldErr}), nil
	}

	vtctld := vtadminvtctldclient.New(&vtadminvtctldclient.Config{
		Cluster:   cluster.ToProto(),
		Discovery: disco,
	})
	vtctld.DialFunc = func(addr string, ff grpcclient.FailFast, opts ...grpc.DialOption) (vtctldclient.VtctldClient, error) {
		return vtctldClient, nil
	}

	cluster.DB = db
	cluster.Vtctld = vtctld

	return cluster
}
