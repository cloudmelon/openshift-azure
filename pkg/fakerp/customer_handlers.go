package fakerp

import (
	"fmt"
	"net/http"

	internalapi "github.com/openshift/openshift-azure/pkg/api"
	armconst "github.com/openshift/openshift-azure/pkg/fakerp/arm/constants"
	"github.com/openshift/openshift-azure/pkg/fakerp/client"
)

func (s *Server) handleDelete(w http.ResponseWriter, req *http.Request) {
	cs := req.Context().Value(contextKeyContainerService).(*internalapi.OpenShiftManagedCluster)
	config := req.Context().Value(contextKeyConfig).(*client.Config)

	cs.Properties.ProvisioningState = internalapi.Deleting
	s.store.Put(cs)

	s.log.Info("creating clients")
	clients, err := newClients(req.Context(), s.log, cs, s.testConfig, config)
	if err != nil {
		s.badRequest(w, fmt.Sprintf("Failed to create clients: %v", err))
		return
	}

	s.log.Info("deleting service principals")
	err = clients.aadMgr.deleteApps(req.Context())
	if err != nil {
		s.badRequest(w, fmt.Sprintf("Failed to delete service principals: %v", err))
		return
	}

	s.log.Info("deleting dns records")
	err = clients.dnsMgr.deleteDns(req.Context(), cs)
	if err != nil {
		s.badRequest(w, fmt.Sprintf("Failed to delete dns records: %v", err))
		return
	}

	s.log.Info("delete pe resources")
	err = clients.netMgr.deletePEs(req.Context(), fmt.Sprintf("%s-%s", armconst.PrivateEndpointNamePrefix, cs.Name))
	if err != nil {
		s.badRequest(w, fmt.Sprintf("Failed to delete pe resources: %v", err))
		return
	}

	s.log.Infof("deleting resource group")
	err = clients.groupClient.Delete(req.Context(), cs.Properties.AzProfile.ResourceGroup)
	if err != nil {
		s.badRequest(w, fmt.Sprintf("Failed to delete resource group: %v", err))
		return
	}

	s.store.Delete()
}

func (s *Server) handleGet(w http.ResponseWriter, req *http.Request) {
	cs := req.Context().Value(contextKeyContainerService).(*internalapi.OpenShiftManagedCluster)
	s.reply(w, req, cs)
}

func (s *Server) handlePut(w http.ResponseWriter, req *http.Request) {
	oldCs := req.Context().Value(contextKeyContainerService).(*internalapi.OpenShiftManagedCluster)

	isAdmin := isAdminRequest(req)

	var apiVersion string
	keys, ok := req.URL.Query()["api-version"]
	if !ok || len(keys[0]) < 1 {
		apiVersion = latestApiVersion
	} else {
		apiVersion = keys[0]
	}

	// convert the external API manifest into the internal API representation
	s.log.Info("read request and convert to internal")
	var cs *internalapi.OpenShiftManagedCluster
	var err error
	if isAdmin {
		s.log.Info("admin request")
		cs, err = s.readAdminRequest(req.Body, oldCs)
		if err == nil {
			cs.Properties.ProvisioningState = internalapi.AdminUpdating
			s.store.Put(cs)
		}
	} else {
		switch apiVersion {
		case "2019-10-27-preview":
			cs, err = s.read20191027Request(req.Body, oldCs)
			if err == nil {
				cs.Properties.ProvisioningState = internalapi.Updating
				s.store.Put(cs)
			}
		case "2019-04-30":
			cs, err = s.read20190430Request(req.Body, oldCs)
			if err == nil {
				cs.Properties.ProvisioningState = internalapi.Updating
				s.store.Put(cs)
			}
		case "2019-09-30-preview":
			cs, err = s.read20190930Request(req.Body, oldCs)
			if err == nil {
				cs.Properties.ProvisioningState = internalapi.Updating
				s.store.Put(cs)
			}
		default:
			s.badRequest(w, fmt.Sprintf("Not supported APIVersion %s", apiVersion))
			return
		}
	}

	config, err := client.NewServerConfig(s.log, cs)
	if err != nil {
		return
	}

	// apply the request
	newCS, err := createOrUpdateWrapper(req.Context(), s.plugin, s.log, cs, oldCs, isAdmin, config, s.testConfig)
	if err != nil {
		cs.Properties.ProvisioningState = internalapi.Failed
		s.store.Put(cs)
		s.badRequest(w, fmt.Sprintf("Failed to apply request: %v", err))
		return
	}
	cs = newCS
	cs.Properties.ProvisioningState = internalapi.Succeeded
	s.store.Put(cs)

	s.reply(w, req, cs)
}
