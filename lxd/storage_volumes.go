package main

import (
	"bytes"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/project"
	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/lxd/state"
	storagePools "github.com/lxc/lxd/lxd/storage"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/version"
)

var storagePoolVolumesCmd = APIEndpoint{
	Path: "storage-pools/{name}/volumes",

	Get:  APIEndpointAction{Handler: storagePoolVolumesGet, AccessHandler: allowProjectPermission("storage-volumes", "view")},
	Post: APIEndpointAction{Handler: storagePoolVolumesPost, AccessHandler: allowProjectPermission("storage-volumes", "manage-storage-volumes")},
}

var storagePoolVolumesTypeCmd = APIEndpoint{
	Path: "storage-pools/{name}/volumes/{type}",

	Get:  APIEndpointAction{Handler: storagePoolVolumesTypeGet, AccessHandler: allowProjectPermission("storage-volumes", "view")},
	Post: APIEndpointAction{Handler: storagePoolVolumesTypePost, AccessHandler: allowProjectPermission("storage-volumes", "manage-storage-volumes")},
}

var storagePoolVolumeTypeContainerCmd = APIEndpoint{
	Path: "storage-pools/{pool}/volumes/container/{name:.*}",

	Delete: APIEndpointAction{Handler: storagePoolVolumeTypeContainerDelete, AccessHandler: allowProjectPermission("storage-volumes", "manage-storage-volumes")},
	Get:    APIEndpointAction{Handler: storagePoolVolumeTypeContainerGet, AccessHandler: allowProjectPermission("storage-volumes", "view")},
	Patch:  APIEndpointAction{Handler: storagePoolVolumeTypeContainerPatch, AccessHandler: allowProjectPermission("storage-volumes", "manage-storage-volumes")},
	Post:   APIEndpointAction{Handler: storagePoolVolumeTypeContainerPost, AccessHandler: allowProjectPermission("storage-volumes", "manage-storage-volumes")},
	Put:    APIEndpointAction{Handler: storagePoolVolumeTypeContainerPut, AccessHandler: allowProjectPermission("storage-volumes", "manage-storage-volumes")},
}

var storagePoolVolumeTypeVMCmd = APIEndpoint{
	Path: "storage-pools/{pool}/volumes/virtual-machine/{name:.*}",

	Delete: APIEndpointAction{Handler: storagePoolVolumeTypeVMDelete, AccessHandler: allowProjectPermission("storage-volumes", "manage-storage-volumes")},
	Get:    APIEndpointAction{Handler: storagePoolVolumeTypeVMGet, AccessHandler: allowProjectPermission("storage-volumes", "view")},
	Patch:  APIEndpointAction{Handler: storagePoolVolumeTypeVMPatch, AccessHandler: allowProjectPermission("storage-volumes", "manage-storage-volumes")},
	Post:   APIEndpointAction{Handler: storagePoolVolumeTypeVMPost, AccessHandler: allowProjectPermission("storage-volumes", "manage-storage-volumes")},
	Put:    APIEndpointAction{Handler: storagePoolVolumeTypeVMPut, AccessHandler: allowProjectPermission("storage-volumes", "manage-storage-volumes")},
}

var storagePoolVolumeTypeCustomCmd = APIEndpoint{
	Path: "storage-pools/{pool}/volumes/custom/{name}",

	Delete: APIEndpointAction{Handler: storagePoolVolumeTypeCustomDelete, AccessHandler: allowProjectPermission("storage-volumes", "manage-storage-volumes")},
	Get:    APIEndpointAction{Handler: storagePoolVolumeTypeCustomGet, AccessHandler: allowProjectPermission("storage-volumes", "view")},
	Patch:  APIEndpointAction{Handler: storagePoolVolumeTypeCustomPatch, AccessHandler: allowProjectPermission("storage-volumes", "manage-storage-volumes")},
	Post:   APIEndpointAction{Handler: storagePoolVolumeTypeCustomPost, AccessHandler: allowProjectPermission("storage-volumes", "manage-storage-volumes")},
	Put:    APIEndpointAction{Handler: storagePoolVolumeTypeCustomPut, AccessHandler: allowProjectPermission("storage-volumes", "manage-storage-volumes")},
}

var storagePoolVolumeTypeImageCmd = APIEndpoint{
	Path: "storage-pools/{pool}/volumes/image/{name}",

	Delete: APIEndpointAction{Handler: storagePoolVolumeTypeImageDelete, AccessHandler: allowProjectPermission("storage-volumes", "manage-storage-volumes")},
	Get:    APIEndpointAction{Handler: storagePoolVolumeTypeImageGet, AccessHandler: allowProjectPermission("storage-volumes", "view")},
	Patch:  APIEndpointAction{Handler: storagePoolVolumeTypeImagePatch, AccessHandler: allowProjectPermission("storage-volumes", "manage-storage-volumes")},
	Post:   APIEndpointAction{Handler: storagePoolVolumeTypeImagePost, AccessHandler: allowProjectPermission("storage-volumes", "manage-storage-volumes")},
	Put:    APIEndpointAction{Handler: storagePoolVolumeTypeImagePut, AccessHandler: allowProjectPermission("storage-volumes", "manage-storage-volumes")},
}

// /1.0/storage-pools/{name}/volumes
// List all storage volumes attached to a given storage pool.
func storagePoolVolumesGet(d *Daemon, r *http.Request) response.Response {
	projectName := projectParam(r)

	poolName := mux.Vars(r)["name"]

	recursion := util.IsRecursionRequest(r)

	// Retrieve ID of the storage pool (and check if the storage pool exists).
	poolID, err := d.cluster.GetStoragePoolID(poolName)
	if err != nil {
		return response.SmartError(err)
	}

	// Get all instance volumes currently attached to the storage pool by ID of the pool and project.
	volumes, err := d.cluster.GetStoragePoolVolumes(projectName, poolID, supportedVolumeTypesInstances)
	if err != nil && err != db.ErrNoSuchObject {
		return response.SmartError(err)
	}

	// The project name used for custom volumes varies based on whether the project has the
	// featues.storage.volumes feature enabled.
	customVolProjectName, err := project.StorageVolumeProject(d.State().Cluster, projectName, db.StoragePoolVolumeTypeCustom)
	if err != nil {
		return response.SmartError(err)
	}

	// Get all custom volumes currently attached to the storage pool by ID of the pool and project.
	custVolumes, err := d.cluster.GetStoragePoolVolumes(customVolProjectName, poolID, []int{db.StoragePoolVolumeTypeCustom})
	if err != nil && err != db.ErrNoSuchObject {
		return response.SmartError(err)
	}

	for _, volume := range custVolumes {
		volumes = append(volumes, volume)
	}

	// We exclude volumes of type image, since those are special: they are stored using the storage_volumes
	// table, but are effectively a cache which is not tied to projects, so we always link the to the default
	// project. This means that we want to filter image volumes and return only the ones that have fingerprint
	// matching images actually in use by the project.
	imageVolumes, err := d.cluster.GetStoragePoolVolumes(project.Default, poolID, []int{db.StoragePoolVolumeTypeImage})
	if err != nil && err != db.ErrNoSuchObject {
		return response.SmartError(err)
	}

	projectImages, err := d.cluster.GetImagesFingerprints(projectName, false)
	if err != nil {
		return response.SmartError(err)
	}
	for _, volume := range imageVolumes {
		if shared.StringInSlice(volume.Name, projectImages) {
			volumes = append(volumes, volume)
		}
	}

	resultString := []string{}
	for _, volume := range volumes {
		apiEndpoint, err := storagePoolVolumeTypeNameToAPIEndpoint(volume.Type)
		if err != nil {
			return response.InternalError(err)
		}

		if apiEndpoint == storagePoolVolumeAPIEndpointContainers {
			apiEndpoint = "container"
		} else if apiEndpoint == storagePoolVolumeAPIEndpointVMs {
			apiEndpoint = "virtual-machine"
		} else if apiEndpoint == storagePoolVolumeAPIEndpointImages {
			apiEndpoint = "image"
		}

		if !recursion {
			volName, snapName, ok := shared.InstanceGetParentAndSnapshotName(volume.Name)
			if ok {
				if projectName == project.Default {
					resultString = append(resultString,
						fmt.Sprintf("/%s/storage-pools/%s/volumes/%s/%s/snapshots/%s",
							version.APIVersion, poolName, apiEndpoint, volName, snapName))
				} else {
					resultString = append(resultString,
						fmt.Sprintf("/%s/storage-pools/%s/volumes/%s/%s/snapshots/%s?project=%s",
							version.APIVersion, poolName, apiEndpoint, volName, snapName, projectName))
				}
			} else {
				if projectName == project.Default {
					resultString = append(resultString,
						fmt.Sprintf("/%s/storage-pools/%s/volumes/%s/%s",
							version.APIVersion, poolName, apiEndpoint, volume.Name))
				} else {
					resultString = append(resultString,
						fmt.Sprintf("/%s/storage-pools/%s/volumes/%s/%s?project=%s",
							version.APIVersion, poolName, apiEndpoint, volume.Name, projectName))
				}
			}
		} else {
			volumeUsedBy, err := storagePoolVolumeUsedByGet(d.State(), projectName, poolName, volume.Name, volume.Type)
			if err != nil {
				return response.InternalError(err)
			}
			volume.UsedBy = volumeUsedBy
		}
	}

	if !recursion {
		return response.SyncResponse(true, resultString)
	}

	return response.SyncResponse(true, volumes)
}

// /1.0/storage-pools/{name}/volumes/{type}
// List all storage volumes of a given volume type for a given storage pool.
func storagePoolVolumesTypeGet(d *Daemon, r *http.Request) response.Response {
	// Get the name of the pool the storage volume is supposed to be attached to.
	poolName := mux.Vars(r)["name"]

	// Get the name of the volume type.
	volumeTypeName := mux.Vars(r)["type"]

	recursion := util.IsRecursionRequest(r)

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePools.VolumeTypeNameToType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	// Check that the storage volume type is valid.
	if !shared.IntInSlice(volumeType, supportedVolumeTypes) {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %q", volumeTypeName))
	}

	projectName, err := project.StorageVolumeProject(d.State().Cluster, projectParam(r), volumeType)
	if err != nil {
		return response.SmartError(err)
	}

	// Retrieve ID of the storage pool (and check if the storage pool exists).
	poolID, err := d.cluster.GetStoragePoolID(poolName)
	if err != nil {
		return response.SmartError(err)
	}

	// Get the names of all storage volumes of a given volume type currently attached to the storage pool.
	volumes, err := d.cluster.GetLocalStoragePoolVolumesWithType(projectName, volumeType, poolID)
	if err != nil {
		return response.SmartError(err)
	}

	resultString := []string{}
	resultMap := []*api.StorageVolume{}
	for _, volume := range volumes {
		if !recursion {
			apiEndpoint, err := storagePoolVolumeTypeToAPIEndpoint(volumeType)
			if err != nil {
				return response.InternalError(err)
			}

			if apiEndpoint == storagePoolVolumeAPIEndpointContainers {
				apiEndpoint = "container"
			} else if apiEndpoint == storagePoolVolumeAPIEndpointVMs {
				apiEndpoint = "virtual-machine"
			} else if apiEndpoint == storagePoolVolumeAPIEndpointImages {
				apiEndpoint = "image"
			}

			resultString = append(resultString, fmt.Sprintf("/%s/storage-pools/%s/volumes/%s/%s", version.APIVersion, poolName, apiEndpoint, volume))
		} else {
			_, vol, err := d.cluster.GetLocalStoragePoolVolume(projectName, volume, volumeType, poolID)
			if err != nil {
				continue
			}

			volumeUsedBy, err := storagePoolVolumeUsedByGet(d.State(), projectName, poolName, vol.Name, vol.Type)
			if err != nil {
				return response.SmartError(err)
			}
			vol.UsedBy = volumeUsedBy

			resultMap = append(resultMap, vol)
		}
	}

	if !recursion {
		return response.SyncResponse(true, resultString)
	}

	return response.SyncResponse(true, resultMap)
}

// /1.0/storage-pools/{name}/volumes/{type}
// Create a storage volume in a given storage pool.
func storagePoolVolumesTypePost(d *Daemon, r *http.Request) response.Response {
	resp := forwardedResponseIfTargetIsRemote(d, r)
	if resp != nil {
		return resp
	}

	req := api.StorageVolumesPost{}

	// Parse the request.
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Sanity checks.
	if req.Name == "" {
		return response.BadRequest(fmt.Errorf("No name provided"))
	}

	if strings.Contains(req.Name, "/") {
		return response.BadRequest(fmt.Errorf("Storage volume names may not contain slashes"))
	}

	// Backward compatibility.
	if req.ContentType == "" {
		req.ContentType = "filesystem"
	}

	_, err = storagePools.VolumeContentTypeNameToContentType(req.ContentType)
	if err != nil {
		return response.BadRequest(fmt.Errorf("Invalid content type %q", req.ContentType))
	}

	req.Type = mux.Vars(r)["type"]

	// We currently only allow to create storage volumes of type storagePoolVolumeTypeCustom.
	// So check, that nothing else was requested.
	if req.Type != db.StoragePoolVolumeTypeNameCustom {
		return response.BadRequest(fmt.Errorf(`Currently not allowed to create storage volumes of type %q`, req.Type))
	}

	projectName, err := project.StorageVolumeProject(d.State().Cluster, projectParam(r), db.StoragePoolVolumeTypeCustom)
	if err != nil {
		return response.SmartError(err)
	}

	poolName := mux.Vars(r)["name"]
	poolID, err := d.cluster.GetStoragePoolID(poolName)
	if err != nil {
		return response.SmartError(err)
	}

	// Check if destination volume exists.
	_, _, err = d.cluster.GetLocalStoragePoolVolume(projectName, req.Name, db.StoragePoolVolumeTypeCustom, poolID)
	if err != db.ErrNoSuchObject {
		if err != nil {
			return response.SmartError(err)
		}

		return response.Conflict(fmt.Errorf("Volume by that name already exists"))
	}

	switch req.Source.Type {
	case "":
		return doVolumeCreateOrCopy(d, projectName, poolName, &req)
	case "copy":
		return doVolumeCreateOrCopy(d, projectName, poolName, &req)
	case "migration":
		return doVolumeMigration(d, projectName, poolName, &req)
	default:
		return response.BadRequest(fmt.Errorf("Unknown source type %q", req.Source.Type))
	}
}

func doVolumeCreateOrCopy(d *Daemon, projectName, poolName string, req *api.StorageVolumesPost) response.Response {
	var run func(op *operations.Operation) error

	pool, err := storagePools.GetPoolByName(d.State(), poolName)
	if err != nil {
		return response.SmartError(err)
	}

	volumeDBContentType, err := storagePools.VolumeContentTypeNameToContentType(req.ContentType)
	if err != nil {
		return response.SmartError(err)
	}

	contentType, err := storagePools.VolumeDBContentTypeToContentType(volumeDBContentType)
	if err != nil {
		return response.SmartError(err)
	}

	run = func(op *operations.Operation) error {
		if req.Source.Name == "" {
			return pool.CreateCustomVolume(projectName, req.Name, req.Description, req.Config, contentType, op)
		}

		return pool.CreateCustomVolumeFromCopy(projectName, req.Name, req.Description, req.Config, req.Source.Pool, req.Source.Name, req.Source.VolumeOnly, op)
	}

	// If no source name supplied then this a volume create operation.
	if req.Source.Name == "" {
		err := run(nil)
		if err != nil {
			return response.SmartError(err)
		}

		return response.EmptySyncResponse
	}

	// Volume copy operations potentially take a long time, so run as an async operation.
	op, err := operations.OperationCreate(d.State(), "", operations.OperationClassTask, db.OperationVolumeCopy, nil, nil, run, nil, nil)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

// /1.0/storage-pools/{name}/volumes/{type}
// Create a storage volume of a given volume type in a given storage pool.
func storagePoolVolumesPost(d *Daemon, r *http.Request) response.Response {
	resp := forwardedResponseIfTargetIsRemote(d, r)
	if resp != nil {
		return resp
	}

	req := api.StorageVolumesPost{}

	// Parse the request.
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Sanity checks.
	if req.Name == "" {
		return response.BadRequest(fmt.Errorf("No name provided"))
	}

	if strings.Contains(req.Name, "/") {
		return response.BadRequest(fmt.Errorf("Storage volume names may not contain slashes"))
	}

	// Check that the user gave use a storage volume type for the storage
	// volume we are about to create.
	if req.Type == "" {
		return response.BadRequest(fmt.Errorf("You must provide a storage volume type of the storage volume"))
	}

	// We currently only allow to create storage volumes of type storagePoolVolumeTypeCustom.
	// So check, that nothing else was requested.
	if req.Type != db.StoragePoolVolumeTypeNameCustom {
		return response.BadRequest(fmt.Errorf(`Currently not allowed to create storage volumes of type %q`, req.Type))
	}

	projectName, err := project.StorageVolumeProject(d.State().Cluster, projectParam(r), db.StoragePoolVolumeTypeCustom)
	if err != nil {
		return response.SmartError(err)
	}

	poolName := mux.Vars(r)["name"]
	poolID, err := d.cluster.GetStoragePoolID(poolName)
	if err != nil {
		return response.SmartError(err)
	}

	// Check if destination volume exists.
	_, _, err = d.cluster.GetLocalStoragePoolVolume(projectName, req.Name, db.StoragePoolVolumeTypeCustom, poolID)
	if err != db.ErrNoSuchObject {
		if err != nil {
			return response.SmartError(err)
		}

		return response.Conflict(fmt.Errorf("Volume by that name already exists"))
	}

	switch req.Source.Type {
	case "":
		return doVolumeCreateOrCopy(d, projectName, poolName, &req)
	case "copy":
		return doVolumeCreateOrCopy(d, projectName, poolName, &req)
	case "migration":
		return doVolumeMigration(d, projectName, poolName, &req)
	default:
		return response.BadRequest(fmt.Errorf("Unknown source type %q", req.Source.Type))
	}
}

func doVolumeMigration(d *Daemon, projectName string, poolName string, req *api.StorageVolumesPost) response.Response {
	// Validate migration mode
	if req.Source.Mode != "pull" && req.Source.Mode != "push" {
		return response.NotImplemented(fmt.Errorf("Mode '%s' not implemented", req.Source.Mode))
	}

	// create new certificate
	var err error
	var cert *x509.Certificate
	if req.Source.Certificate != "" {
		certBlock, _ := pem.Decode([]byte(req.Source.Certificate))
		if certBlock == nil {
			return response.InternalError(fmt.Errorf("Invalid certificate"))
		}

		cert, err = x509.ParseCertificate(certBlock.Bytes)
		if err != nil {
			return response.InternalError(err)
		}
	}

	config, err := shared.GetTLSConfig("", "", "", cert)
	if err != nil {
		return response.InternalError(err)
	}

	push := false
	if req.Source.Mode == "push" {
		push = true
	}

	// Initialise migrationArgs, don't set the Storage property yet, this is done in DoStorage,
	// to avoid this function relying on the legacy storage layer.
	migrationArgs := MigrationSinkArgs{
		Url: req.Source.Operation,
		Dialer: websocket.Dialer{
			TLSClientConfig: config,
			NetDial:         shared.RFC3493Dialer,
		},
		Secrets:    req.Source.Websockets,
		Push:       push,
		VolumeOnly: req.Source.VolumeOnly,
	}

	sink, err := newStorageMigrationSink(&migrationArgs)
	if err != nil {
		return response.InternalError(err)
	}

	resources := map[string][]string{}
	resources["storage_volumes"] = []string{fmt.Sprintf("%s/volumes/custom/%s", poolName, req.Name)}

	run := func(op *operations.Operation) error {
		// And finally run the migration.
		err = sink.DoStorage(d.State(), projectName, poolName, req, op)
		if err != nil {
			logger.Error("Error during migration sink", log.Ctx{"err": err})
			return fmt.Errorf("Error transferring storage volume: %s", err)
		}

		return nil
	}

	var op *operations.Operation
	if push {
		op, err = operations.OperationCreate(d.State(), "", operations.OperationClassWebsocket, db.OperationVolumeCreate, resources, sink.Metadata(), run, nil, sink.Connect)
		if err != nil {
			return response.InternalError(err)
		}
	} else {
		op, err = operations.OperationCreate(d.State(), "", operations.OperationClassTask, db.OperationVolumeCopy, resources, nil, run, nil, nil)
		if err != nil {
			return response.InternalError(err)
		}
	}

	return operations.OperationResponse(op)
}

// /1.0/storage-pools/{name}/volumes/{type}/{name}
// Rename a storage volume of a given volume type in a given storage pool.
// Also supports moving a storage volume between pools and migrating to a different host.
func storagePoolVolumeTypePost(d *Daemon, r *http.Request, volumeTypeName string) response.Response {
	// Get the name of the storage volume.
	volumeName := mux.Vars(r)["name"]

	if shared.IsSnapshot(volumeName) {
		return response.BadRequest(fmt.Errorf("Invalid volume name"))
	}

	// Get the name of the storage pool the volume is supposed to be attached to.
	poolName := mux.Vars(r)["pool"]

	req := api.StorageVolumePost{}

	// Parse the request.
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Sanity checks.
	if req.Name == "" {
		return response.BadRequest(fmt.Errorf("No name provided"))
	}

	// Check requested new volume name is not a snapshot volume.
	if shared.IsSnapshot(req.Name) {
		return response.BadRequest(fmt.Errorf("Storage volume names may not contain slashes"))
	}

	// We currently only allow to create storage volumes of type storagePoolVolumeTypeCustom.
	// So check, that nothing else was requested.
	if volumeTypeName != db.StoragePoolVolumeTypeNameCustom {
		return response.BadRequest(fmt.Errorf("Renaming storage volumes of type %q is not allowed", volumeTypeName))
	}

	projectName, err := project.StorageVolumeProject(d.State().Cluster, projectParam(r), db.StoragePoolVolumeTypeCustom)
	if err != nil {
		return response.SmartError(err)
	}

	// Retrieve ID of the storage pool (and check if the storage pool exists).
	var poolID int64
	if req.Pool != "" {
		poolID, err = d.cluster.GetStoragePoolID(req.Pool)
	} else {
		poolID, err = d.cluster.GetStoragePoolID(poolName)
	}
	if err != nil {
		return response.SmartError(err)
	}

	// We need to restore the body of the request since it has already been read, and if we
	// forwarded it now no body would be written out.
	buf := bytes.Buffer{}
	err = json.NewEncoder(&buf).Encode(req)
	if err != nil {
		return response.SmartError(err)
	}
	r.Body = shared.BytesReadCloser{Buf: &buf}

	resp := forwardedResponseIfTargetIsRemote(d, r)
	if resp != nil {
		return resp
	}

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePools.VolumeTypeNameToType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	resp = forwardedResponseIfVolumeIsRemote(d, r, poolID, volumeName, volumeType)
	if resp != nil {
		return resp
	}

	// This is a migration request so send back requested secrets.
	if req.Migration {
		return storagePoolVolumeTypePostMigration(d.State(), projectName, poolName, volumeName, req)
	}

	// Check that the name isn't already in use.
	_, err = d.cluster.GetStoragePoolNodeVolumeID(projectName, req.Name, volumeType, poolID)
	if err != db.ErrNoSuchObject {
		if err != nil {
			return response.InternalError(err)
		}

		return response.Conflict(fmt.Errorf("Volume by that name already exists"))
	}

	// Check if the daemon itself is using it.
	used, err := storagePools.VolumeUsedByDaemon(d.State(), poolName, volumeName)
	if err != nil {
		return response.SmartError(err)
	}

	if used {
		return response.SmartError(fmt.Errorf("Volume is used by LXD itself and cannot be renamed"))
	}

	// Check if a running container is using it.
	ctsUsingVolume, err := storagePools.VolumeUsedByRunningInstancesWithProfilesGet(d.State(), projectName, poolName, volumeName, volumeTypeName, true)
	if err != nil {
		return response.SmartError(err)
	}

	if len(ctsUsingVolume) > 0 {
		return response.SmartError(fmt.Errorf("Volume is still in use by running instances"))
	}

	// Detect a rename request.
	if req.Pool == "" || req.Pool == poolName {
		return storagePoolVolumeTypePostRename(d, projectName, poolName, volumeName, volumeType, req)
	}

	// Otherwise this is a move request.
	return storagePoolVolumeTypePostMove(d, projectName, poolName, volumeName, volumeType, req)
}

// storagePoolVolumeTypePostMigration handles volume migration type POST requests.
func storagePoolVolumeTypePostMigration(state *state.State, projectName, poolName, volumeName string, req api.StorageVolumePost) response.Response {
	ws, err := newStorageMigrationSource(req.VolumeOnly)
	if err != nil {
		return response.InternalError(err)
	}

	resources := map[string][]string{}
	resources["storage_volumes"] = []string{fmt.Sprintf("%s/volumes/custom/%s", poolName, volumeName)}

	run := func(op *operations.Operation) error {
		return ws.DoStorage(state, projectName, poolName, volumeName, op)
	}

	if req.Target != nil {
		// Push mode
		err := ws.ConnectStorageTarget(*req.Target)
		if err != nil {
			return response.InternalError(err)
		}

		op, err := operations.OperationCreate(state, "", operations.OperationClassTask, db.OperationVolumeMigrate, resources, nil, run, nil, nil)
		if err != nil {
			return response.InternalError(err)
		}

		return operations.OperationResponse(op)
	}

	// Pull mode
	op, err := operations.OperationCreate(state, "", operations.OperationClassWebsocket, db.OperationVolumeMigrate, resources, ws.Metadata(), run, nil, ws.Connect)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

// storagePoolVolumeTypePostRename handles volume rename type POST requests.
func storagePoolVolumeTypePostRename(d *Daemon, projectName, poolName, volumeName string, volumeType int, req api.StorageVolumePost) response.Response {
	// Notify users of the volume that it's name is changing.
	err := storagePoolVolumeUpdateUsers(d, projectName, poolName, volumeName, req.Pool, req.Name)
	if err != nil {
		return response.SmartError(err)
	}

	pool, err := storagePools.GetPoolByName(d.State(), poolName)
	if err != nil {
		return response.SmartError(err)
	}

	err = pool.RenameCustomVolume(projectName, volumeName, req.Name, nil)
	if err != nil {
		// Notify users of the volume that it's name is changing back.
		storagePoolVolumeUpdateUsers(d, projectName, req.Pool, req.Name, poolName, volumeName)
		return response.SmartError(err)
	}

	return response.SyncResponseLocation(true, nil, fmt.Sprintf("/%s/storage-pools/%s/volumes/%s", version.APIVersion, poolName, storagePoolVolumeAPIEndpointCustom))
}

// storagePoolVolumeTypePostMove handles volume move type POST requests.
func storagePoolVolumeTypePostMove(d *Daemon, projectName, poolName, volumeName string, volumeType int, req api.StorageVolumePost) response.Response {
	var run func(op *operations.Operation) error

	srcPool, err := storagePools.GetPoolByName(d.State(), poolName)
	if err != nil {
		return response.SmartError(err)
	}

	pool, err := storagePools.GetPoolByName(d.State(), req.Pool)
	if err != nil {
		return response.SmartError(err)
	}

	run = func(op *operations.Operation) error {
		// Notify users of the volume that it's name is changing.
		err := storagePoolVolumeUpdateUsers(d, projectName, poolName, volumeName, req.Pool, req.Name)
		if err != nil {
			return err
		}

		// Provide empty description and nil config to instruct
		// CreateCustomVolumeFromCopy to copy it from source volume.
		err = pool.CreateCustomVolumeFromCopy(projectName, req.Name, "", nil, poolName, volumeName, false, op)
		if err != nil {
			// Notify users of the volume that it's name is changing back.
			storagePoolVolumeUpdateUsers(d, projectName, req.Pool, req.Name, poolName, volumeName)
			return err
		}

		return srcPool.DeleteCustomVolume(projectName, volumeName, op)
	}

	op, err := operations.OperationCreate(d.State(), "", operations.OperationClassTask, db.OperationVolumeMove, nil, nil, run, nil, nil)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

func storagePoolVolumeTypeContainerPost(d *Daemon, r *http.Request) response.Response {
	return storagePoolVolumeTypePost(d, r, "container")
}

func storagePoolVolumeTypeVMPost(d *Daemon, r *http.Request) response.Response {
	return storagePoolVolumeTypePost(d, r, "virtual-machine")
}

func storagePoolVolumeTypeCustomPost(d *Daemon, r *http.Request) response.Response {
	return storagePoolVolumeTypePost(d, r, "custom")
}

func storagePoolVolumeTypeImagePost(d *Daemon, r *http.Request) response.Response {
	return storagePoolVolumeTypePost(d, r, "image")
}

// storageGetVolumeNameFromURL retrieves the volume name from the URL name segment.
func storageGetVolumeNameFromURL(r *http.Request) (string, error) {
	fields := strings.Split(mux.Vars(r)["name"], "/")

	if len(fields) == 3 && fields[1] == "snapshots" {
		// Handle volume snapshots.
		return fmt.Sprintf("%s%s%s", fields[0], shared.SnapshotDelimiter, fields[2]), nil
	} else if len(fields) > 1 {
		return fmt.Sprintf("%s%s%s", fields[0], shared.SnapshotDelimiter, fields[1]), nil
	} else if len(fields) > 0 {
		// Handle volume.
		return fields[0], nil
	}

	return "", fmt.Errorf("Invalid storage volume %s", mux.Vars(r)["name"])
}

// /1.0/storage-pools/{pool}/volumes/{type}/{name}
// Get storage volume of a given volume type on a given storage pool.
func storagePoolVolumeTypeGet(d *Daemon, r *http.Request, volumeTypeName string) response.Response {
	// Get the name of the storage volume.
	volumeName, err := storageGetVolumeNameFromURL(r)
	if err != nil {
		return response.BadRequest(err)
	}

	// Get the name of the storage pool the volume is supposed to be attached to.
	poolName := mux.Vars(r)["pool"]

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePools.VolumeTypeNameToType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	// Check that the storage volume type is valid.
	if !shared.IntInSlice(volumeType, supportedVolumeTypes) {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %q", volumeTypeName))
	}

	projectName, err := project.StorageVolumeProject(d.State().Cluster, projectParam(r), db.StoragePoolVolumeTypeCustom)
	if err != nil {
		return response.SmartError(err)
	}

	// Get the ID of the storage pool the storage volume is supposed to be attached to.
	poolID, err := d.cluster.GetStoragePoolID(poolName)
	if err != nil {
		return response.SmartError(err)
	}

	resp := forwardedResponseIfTargetIsRemote(d, r)
	if resp != nil {
		return resp
	}

	resp = forwardedResponseIfVolumeIsRemote(d, r, poolID, volumeName, volumeType)
	if resp != nil {
		return resp
	}

	// Get the storage volume.
	_, volume, err := d.cluster.GetLocalStoragePoolVolume(projectName, volumeName, volumeType, poolID)
	if err != nil {
		return response.SmartError(err)
	}

	volumeUsedBy, err := storagePoolVolumeUsedByGet(d.State(), projectName, poolName, volume.Name, volume.Type)
	if err != nil {
		return response.SmartError(err)
	}
	volume.UsedBy = volumeUsedBy

	etag := []interface{}{volumeName, volume.Type, volume.Config}

	return response.SyncResponseETag(true, volume, etag)
}

func storagePoolVolumeTypeContainerGet(d *Daemon, r *http.Request) response.Response {
	return storagePoolVolumeTypeGet(d, r, "container")
}

func storagePoolVolumeTypeVMGet(d *Daemon, r *http.Request) response.Response {
	return storagePoolVolumeTypeGet(d, r, "virtual-machine")
}

func storagePoolVolumeTypeCustomGet(d *Daemon, r *http.Request) response.Response {
	return storagePoolVolumeTypeGet(d, r, "custom")
}

func storagePoolVolumeTypeImageGet(d *Daemon, r *http.Request) response.Response {
	return storagePoolVolumeTypeGet(d, r, "image")
}

// /1.0/storage-pools/{pool}/volumes/{type}/{name}
// This function does allow limited functionality for non-custom volume types, specifically you
// can modify the volume's description only.
func storagePoolVolumeTypePut(d *Daemon, r *http.Request, volumeTypeName string) response.Response {
	projectName := projectParam(r)

	// Get the name of the storage volume.
	volumeName, err := storageGetVolumeNameFromURL(r)
	if err != nil {
		return response.BadRequest(err)
	}

	// Get the name of the storage pool the volume is supposed to be attached to.
	poolName := mux.Vars(r)["pool"]

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePools.VolumeTypeNameToType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	projectName, err = project.StorageVolumeProject(d.State().Cluster, projectName, volumeType)
	if err != nil {
		return response.SmartError(err)
	}

	// Check that the storage volume type is valid.
	if !shared.IntInSlice(volumeType, supportedVolumeTypes) {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %q", volumeTypeName))
	}

	pool, err := storagePools.GetPoolByName(d.State(), poolName)
	if err != nil {
		return response.SmartError(err)
	}

	resp := forwardedResponseIfTargetIsRemote(d, r)
	if resp != nil {
		return resp
	}

	resp = forwardedResponseIfVolumeIsRemote(d, r, pool.ID(), volumeName, volumeType)
	if resp != nil {
		return resp
	}

	// Get the existing storage volume.
	_, vol, err := d.cluster.GetLocalStoragePoolVolume(projectName, volumeName, volumeType, pool.ID())
	if err != nil {
		return response.SmartError(err)
	}

	// Validate the ETag
	etag := []interface{}{volumeName, vol.Type, vol.Config}

	err = util.EtagCheck(r, etag)
	if err != nil {
		return response.PreconditionFailed(err)
	}

	req := api.StorageVolumePut{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return response.BadRequest(err)
	}

	if volumeType == db.StoragePoolVolumeTypeCustom {
		// Restore custom volume from snapshot if requested. This should occur first
		// before applying config changes so that changes are applied to the
		// restored volume.
		if req.Restore != "" {
			err = pool.RestoreCustomVolume(projectName, vol.Name, req.Restore, nil)
			if err != nil {
				return response.SmartError(err)
			}
		}

		// Handle custom volume update requests.
		err = pool.UpdateCustomVolume(projectName, vol.Name, req.Description, req.Config, nil)
		if err != nil {
			return response.SmartError(err)
		}
	} else if volumeType == db.StoragePoolVolumeTypeContainer || volumeType == db.StoragePoolVolumeTypeVM {
		inst, err := instance.LoadByProjectAndName(d.State(), projectName, vol.Name)
		if err != nil {
			return response.NotFound(err)
		}

		// There is a bug in the lxc client (lxc/storage_volume.go#L829-L865) which
		// means that modifying an instance snapshot's description gets routed here
		// rather than the dedicated snapshot editing route. So need to handle
		// snapshot volumes here too.
		if inst.IsSnapshot() {
			// Handle instance snapshot volume update requests.
			err = pool.UpdateInstanceSnapshot(inst, req.Description, req.Config, nil)
			if err != nil {
				return response.SmartError(err)
			}
		} else {
			// Handle instance volume update requests.
			err = pool.UpdateInstance(inst, req.Description, req.Config, nil)
			if err != nil {
				return response.SmartError(err)
			}
		}
	} else if volumeType == db.StoragePoolVolumeTypeImage {
		// Handle image update requests.
		err = pool.UpdateImage(vol.Name, req.Description, req.Config, nil)
		if err != nil {
			return response.SmartError(err)
		}
	} else {
		return response.SmartError(fmt.Errorf("Invalid volume type"))
	}

	return response.EmptySyncResponse
}

func storagePoolVolumeTypeContainerPut(d *Daemon, r *http.Request) response.Response {
	return storagePoolVolumeTypePut(d, r, "container")
}

func storagePoolVolumeTypeVMPut(d *Daemon, r *http.Request) response.Response {
	return storagePoolVolumeTypePut(d, r, "virtual-machine")
}

func storagePoolVolumeTypeCustomPut(d *Daemon, r *http.Request) response.Response {
	return storagePoolVolumeTypePut(d, r, "custom")
}

func storagePoolVolumeTypeImagePut(d *Daemon, r *http.Request) response.Response {
	return storagePoolVolumeTypePut(d, r, "image")
}

// /1.0/storage-pools/{pool}/volumes/{type}/{name}
func storagePoolVolumeTypePatch(d *Daemon, r *http.Request, volumeTypeName string) response.Response {
	// Get the name of the storage volume.
	volumeName := mux.Vars(r)["name"]

	if shared.IsSnapshot(volumeName) {
		return response.BadRequest(fmt.Errorf("Invalid volume name"))
	}

	// Get the name of the storage pool the volume is supposed to be attached to.
	poolName := mux.Vars(r)["pool"]

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePools.VolumeTypeNameToType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	// Check that the storage volume type is custom.
	if volumeType != db.StoragePoolVolumeTypeCustom {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %q", volumeTypeName))
	}

	projectName, err := project.StorageVolumeProject(d.State().Cluster, projectParam(r), volumeType)
	if err != nil {
		return response.SmartError(err)
	}

	pool, err := storagePools.GetPoolByName(d.State(), poolName)
	if err != nil {
		return response.SmartError(err)
	}

	resp := forwardedResponseIfTargetIsRemote(d, r)
	if resp != nil {
		return resp
	}

	resp = forwardedResponseIfVolumeIsRemote(d, r, pool.ID(), volumeName, volumeType)
	if resp != nil {
		return resp
	}

	// Get the existing storage volume.
	_, vol, err := d.cluster.GetLocalStoragePoolVolume(projectName, volumeName, volumeType, pool.ID())
	if err != nil {
		return response.SmartError(err)
	}

	// Validate the ETag.
	etag := []interface{}{volumeName, vol.Type, vol.Config}

	err = util.EtagCheck(r, etag)
	if err != nil {
		return response.PreconditionFailed(err)
	}

	req := api.StorageVolumePut{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return response.BadRequest(err)
	}

	if req.Config == nil {
		req.Config = map[string]string{}
	}

	// Merge current config with requested changes.
	for k, v := range vol.Config {
		_, ok := req.Config[k]
		if !ok {
			req.Config[k] = v
		}
	}

	err = pool.UpdateCustomVolume(projectName, vol.Name, req.Description, req.Config, nil)
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

func storagePoolVolumeTypeContainerPatch(d *Daemon, r *http.Request) response.Response {
	return storagePoolVolumeTypePatch(d, r, "container")
}

func storagePoolVolumeTypeVMPatch(d *Daemon, r *http.Request) response.Response {
	return storagePoolVolumeTypePatch(d, r, "virtual-machine")
}

func storagePoolVolumeTypeCustomPatch(d *Daemon, r *http.Request) response.Response {
	return storagePoolVolumeTypePatch(d, r, "custom")
}

func storagePoolVolumeTypeImagePatch(d *Daemon, r *http.Request) response.Response {
	return storagePoolVolumeTypePatch(d, r, "image")
}

// /1.0/storage-pools/{pool}/volumes/{type}/{name}
func storagePoolVolumeTypeDelete(d *Daemon, r *http.Request, volumeTypeName string) response.Response {
	// Get the name of the storage volume.
	volumeName := mux.Vars(r)["name"]

	if shared.IsSnapshot(volumeName) {
		return response.BadRequest(fmt.Errorf("Invalid storage volume %q", volumeName))
	}

	// Get the name of the storage pool the volume is supposed to be attached to.
	poolName := mux.Vars(r)["pool"]

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePools.VolumeTypeNameToType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	projectName, err := project.StorageVolumeProject(d.State().Cluster, projectParam(r), volumeType)
	if err != nil {
		return response.SmartError(err)
	}

	// Check that the storage volume type is valid.
	if !shared.IntInSlice(volumeType, supportedVolumeTypes) {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %q", volumeTypeName))
	}

	resp := forwardedResponseIfTargetIsRemote(d, r)
	if resp != nil {
		return resp
	}

	poolID, _, err := d.cluster.GetStoragePool(poolName)
	if err != nil {
		return response.SmartError(err)
	}

	resp = forwardedResponseIfVolumeIsRemote(d, r, poolID, volumeName, volumeType)
	if resp != nil {
		return resp
	}

	if volumeType != db.StoragePoolVolumeTypeCustom && volumeType != db.StoragePoolVolumeTypeImage {
		return response.BadRequest(fmt.Errorf("Storage volumes of type %q cannot be deleted with the storage API", volumeTypeName))
	}

	volumeUsedBy, err := storagePoolVolumeUsedByGet(d.State(), projectName, poolName, volumeName, volumeTypeName)
	if err != nil {
		return response.SmartError(err)
	}

	if len(volumeUsedBy) > 0 {
		if len(volumeUsedBy) != 1 || volumeType != db.StoragePoolVolumeTypeImage || volumeUsedBy[0] != fmt.Sprintf("/%s/images/%s", version.APIVersion, volumeName) {
			return response.BadRequest(fmt.Errorf("The storage volume is still in use"))
		}
	}

	pool, err := storagePools.GetPoolByName(d.State(), poolName)
	if err != nil {
		return response.SmartError(err)
	}

	switch volumeType {
	case db.StoragePoolVolumeTypeCustom:
		err = pool.DeleteCustomVolume(projectName, volumeName, nil)
	case db.StoragePoolVolumeTypeImage:
		err = pool.DeleteImage(volumeName, nil)
	default:
		return response.BadRequest(fmt.Errorf(`Storage volumes of type %q cannot be deleted with the storage API`, volumeTypeName))
	}
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

func storagePoolVolumeTypeContainerDelete(d *Daemon, r *http.Request) response.Response {
	return storagePoolVolumeTypeDelete(d, r, "container")
}

func storagePoolVolumeTypeVMDelete(d *Daemon, r *http.Request) response.Response {
	return storagePoolVolumeTypeDelete(d, r, "virtual-machine")
}

func storagePoolVolumeTypeCustomDelete(d *Daemon, r *http.Request) response.Response {
	return storagePoolVolumeTypeDelete(d, r, "custom")
}

func storagePoolVolumeTypeImageDelete(d *Daemon, r *http.Request) response.Response {
	return storagePoolVolumeTypeDelete(d, r, "image")
}
