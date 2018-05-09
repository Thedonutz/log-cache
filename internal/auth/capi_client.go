package auth

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"

	"github.com/bluele/gcache"
)

type CAPIClient struct {
	client       HTTPClient
	capi         string
	externalCapi string
	cache        gcache.Cache
}

func NewCAPIClient(capiAddr, externalCapiAddr string, client HTTPClient) *CAPIClient {
	_, err := url.Parse(capiAddr)
	if err != nil {
		panic(err)
	}

	_, err = url.Parse(externalCapiAddr)
	if err != nil {
		panic(err)
	}

	return &CAPIClient{
		client:       client,
		capi:         capiAddr,
		externalCapi: externalCapiAddr,
		cache:        gcache.New(100).ARC().Build(),
	}
}

func (c *CAPIClient) IsAuthorized(sourceID, token string) (authorized bool) {
	combined := sourceID + token
	value, err := c.cache.Get(combined)
	if err == nil {
		return value.(bool)
	}

	defer func() {
		c.cache.Set(combined, authorized)
	}()

	uri := fmt.Sprintf("%s/internal/v4/log_access/%s", c.capi, sourceID)
	req, err := http.NewRequest(http.MethodGet, uri, nil)
	if err != nil {
		log.Printf("failed to build authorize log access request: %s", err)
		return false
	}

	req.Header.Set("Authorization", token)
	resp, err := c.client.Do(req)
	if err != nil {
		log.Printf("CAPI request failed: %s", err)
		return false
	}

	if resp.StatusCode == http.StatusOK {
		return true
	}

	uri = fmt.Sprintf("%s/v2/service_instances/%s", c.externalCapi, sourceID)
	req, err = http.NewRequest(http.MethodGet, uri, nil)
	if err != nil {
		log.Printf("failed to build authorize service instance access request: %s", err)
		return false
	}

	req.Header.Set("Authorization", token)
	resp, err = c.client.Do(req)
	if err != nil {
		log.Printf("External CAPI request failed: %s", err)
		return false
	}

	return resp.StatusCode == http.StatusOK
}

func (c *CAPIClient) AvailableSourceIDs(token string) []string {
	var sourceIDs []string
	req, err := http.NewRequest(http.MethodGet, c.externalCapi+"/v3/apps", nil)
	if err != nil {
		log.Printf("failed to build authorize log access request: %s", err)
		return nil
	}

	req.Header.Set("Authorization", token)
	resp, err := c.client.Do(req)
	if err != nil {
		log.Printf("CAPI request failed: %s", err)
		return nil
	}
	if resp.StatusCode != http.StatusOK {
		log.Printf("CAPI request failed (/v3/apps): %d", resp.StatusCode)
		return nil
	}

	var appSources struct {
		Resources []struct {
			Guid string `json:"guid"`
		} `json:"resources"`
	}

	json.NewDecoder(resp.Body).Decode(&appSources)

	for _, v := range appSources.Resources {
		sourceIDs = append(sourceIDs, v.Guid)
	}

	req, err = http.NewRequest(http.MethodGet, c.externalCapi+"/v2/service_instances", nil)
	if err != nil {
		log.Printf("failed to build authorize service instance access request: %s", err)
		return nil
	}

	req.Header.Set("Authorization", token)
	resp, err = c.client.Do(req)
	if err != nil {
		log.Printf("External CAPI request failed: %s", err)
		return nil
	}

	if resp.StatusCode != http.StatusOK {
		log.Printf("CAPI request failed (/v2/service_instances): %d", resp.StatusCode)
		return nil
	}

	var serviceSources struct {
		Resources []struct {
			Metadata struct {
				Guid string `json:"guid"`
			} `json:"metadata"`
		} `json:"resources"`
	}

	json.NewDecoder(resp.Body).Decode(&serviceSources)
	for _, v := range serviceSources.Resources {
		sourceIDs = append(sourceIDs, v.Metadata.Guid)
	}

	return sourceIDs
}
