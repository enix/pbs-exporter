package main

import (
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const promNamespace = "pbs"
const datastoreUsageApi = "/api2/json/status/datastore-usage"
const datastoreApi = "/api2/json/admin/datastore"
const nodeApi = "/api2/json/nodes"

var (
	timeoutDuration time.Duration

	tr = &http.Transport{
		TLSClientConfig: &tls.Config{},
	}
	client = &http.Client{
		Transport: tr,
	}

	// Flags
	endpoint = flag.String("pbs.endpoint", "http://localhost:8007",
		"Proxmox Backup Server endpoint")
	username = flag.String("pbs.username", "root@pam",
		"Proxmox Backup Server username")
	apitoken = flag.String("pbs.api.token", "",
		"Proxmox Backup Server API token")
	apitokenname = flag.String("pbs.api.token.name", "pbs-exporter",
		"Proxmox Backup Server API token name")
	timeout = flag.String("pbs.timeout", "5s",
		"Proxmox Backup Server timeout")
	insecure = flag.String("pbs.insecure", "false",
		"Proxmox Backup Server insecure")
	metricsPath = flag.String("pbs.metrics-path", "/metrics",
		"Path under which to expose metrics")
	listenAddress = flag.String("pbs.listen-address", ":9101",
		"Address on which to expose metrics")
	loglevel = flag.String("pbs.loglevel", "info",
		"Loglevel")

	// Metrics
	up = prometheus.NewDesc(
		prometheus.BuildFQName(promNamespace, "", "up"),
		"Was the last query of PBS successful.",
		nil, nil,
	)
	available = prometheus.NewDesc(
		prometheus.BuildFQName(promNamespace, "", "available"),
		"The available bytes of the underlying storage.",
		nil, nil,
	)
	size = prometheus.NewDesc(
		prometheus.BuildFQName(promNamespace, "", "size"),
		"The size of the underlying storage in bytes.",
		nil, nil,
	)
	used = prometheus.NewDesc(
		prometheus.BuildFQName(promNamespace, "", "used"),
		"The used bytes of the underlying storage.",
		nil, nil,
	)
	snapshot_count = prometheus.NewDesc(
		prometheus.BuildFQName(promNamespace, "", "snapshot_count"),
		"The total number of backups.",
		[]string{"namespace"}, nil,
	)
	snapshot_vm_count = prometheus.NewDesc(
		prometheus.BuildFQName(promNamespace, "", "snapshot_vm_count"),
		"The total number of backups per VM.",
		[]string{"namespace", "vm_id"}, nil,
	)
	host_cpu_usage = prometheus.NewDesc(
		prometheus.BuildFQName(promNamespace, "", "host_cpu_usage"),
		"The CPU usage of the host.",
		nil, nil,
	)
	host_memory_free = prometheus.NewDesc(
		prometheus.BuildFQName(promNamespace, "", "host_memory_free"),
		"The free memory of the host.",
		nil, nil,
	)
	host_memory_total = prometheus.NewDesc(
		prometheus.BuildFQName(promNamespace, "", "host_memory_total"),
		"The total memory of the host.",
		nil, nil,
	)
	host_memory_used = prometheus.NewDesc(
		prometheus.BuildFQName(promNamespace, "", "host_memory_used"),
		"The used memory of the host.",
		nil, nil,
	)
	host_swap_free = prometheus.NewDesc(
		prometheus.BuildFQName(promNamespace, "", "host_swap_free"),
		"The free swap of the host.",
		nil, nil,
	)
	host_swap_total = prometheus.NewDesc(
		prometheus.BuildFQName(promNamespace, "", "host_swap_total"),
		"The total swap of the host.",
		nil, nil,
	)
	host_swap_used = prometheus.NewDesc(
		prometheus.BuildFQName(promNamespace, "", "host_swap_used"),
		"The used swap of the host.",
		nil, nil,
	)
	host_disk_available = prometheus.NewDesc(
		prometheus.BuildFQName(promNamespace, "", "host_available_free"),
		"The available disk of the local root disk in bytes.",
		nil, nil,
	)
	host_disk_total = prometheus.NewDesc(
		prometheus.BuildFQName(promNamespace, "", "host_disk_total"),
		"The total disk of the local root disk in bytes.",
		nil, nil,
	)
	host_disk_used = prometheus.NewDesc(
		prometheus.BuildFQName(promNamespace, "", "host_disk_used"),
		"The used disk of the local root disk in bytes.",
		nil, nil,
	)
	host_uptime = prometheus.NewDesc(
		prometheus.BuildFQName(promNamespace, "", "host_uptime"),
		"The uptime of the host.",
		nil, nil,
	)
	host_io_wait = prometheus.NewDesc(
		prometheus.BuildFQName(promNamespace, "", "host_io_wait"),
		"The io wait of the host.",
		nil, nil,
	)
)

type DatastoreResponse struct {
	Data []struct {
		Avail     int64  `json:"avail"`
		Store     string `json:"store"`
		Total     int64  `json:"total"`
		Used      int64  `json:"used"`
		Namespace string `json:"ns"`
	} `json:"data"`
}

type Datastore struct {
	Avail     int64  `json:"avail"`
	Store     string `json:"store"`
	Total     int64  `json:"total"`
	Used      int64  `json:"used"`
	Namespace string `json:"ns"`
}

type NamespaceResponse struct {
	Data []struct {
		Namespace string `json:"ns"`
	} `json:"data"`
}

type SnapshotResponse struct {
	Data []struct {
		BackupID string `json:"backup-id"`
	} `json:"data"`
}

type HostResponse struct {
	Data struct {
		CPU float64 `json:"cpu"`
		Mem struct {
			Free  int64 `json:"free"`
			Total int64 `json:"total"`
			Used  int64 `json:"used"`
		} `json:"memory"`
		Swap struct {
			Free  int64 `json:"free"`
			Total int64 `json:"total"`
			Used  int64 `json:"used"`
		} `json:"swap"`
		Disk struct {
			Avail int64 `json:"avail"`
			Total int64 `json:"total"`
			Used  int64 `json:"used"`
		} `json:"root"`
		Uptime int64   `json:"uptime"`
		Wait   float64 `json:"wait"`
	} `json:"data"`
}

type Exporter struct {
	endpoint            string
	authorizationHeader string
}

func NewExporter(endpoint string, username string, apitoken string, apitokenname string) *Exporter {
	return &Exporter{
		endpoint:            endpoint,
		authorizationHeader: "PBSAPIToken=" + username + "!" + apitokenname + ":" + apitoken,
	}
}

func (e *Exporter) Describe(ch chan<- *prometheus.Desc) {
	ch <- up
	ch <- available
	ch <- size
	ch <- used
	ch <- snapshot_count
	ch <- snapshot_vm_count
	ch <- host_cpu_usage
	ch <- host_memory_free
	ch <- host_memory_total
	ch <- host_memory_used
	ch <- host_swap_free
	ch <- host_swap_total
	ch <- host_swap_used
	ch <- host_disk_available
	ch <- host_disk_total
	ch <- host_disk_used
	ch <- host_uptime
	ch <- host_io_wait
}

func (e *Exporter) Collect(ch chan<- prometheus.Metric) {
	err := e.collectFromAPI(ch)
	if err != nil {
		ch <- prometheus.MustNewConstMetric(
			up, prometheus.GaugeValue, 0,
		)
		log.Println(err)
		return
	}
	ch <- prometheus.MustNewConstMetric(
		up, prometheus.GaugeValue, 1,
	)

}

func (e *Exporter) collectFromAPI(ch chan<- prometheus.Metric) error {
	// get datastores
	req, err := http.NewRequest("GET", e.endpoint+datastoreUsageApi, nil)
	if err != nil {
		return err
	}

	// add Authorization header
	req.Header.Set("Authorization", e.authorizationHeader)

	// debug
	if *loglevel == "debug" {
		log.Printf("DEBUG: Request URL: %s", req.URL)
		log.Printf("DEBUG: Request Header: %s", req.Header)
	}

	// make request and show output
	resp, err := client.Do(req)
	if err != nil {
		return err
	}

	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return err
	}

	// check if status code is 200
	if resp.StatusCode != 200 {
		return fmt.Errorf("ERROR: Status code %d returned from endpoint: %s", resp.StatusCode, e.endpoint)
	}

	// debug
	if *loglevel == "debug" {
		log.Printf("DEBUG: Status code %d returned from endpoint: %s", resp.StatusCode, e.endpoint)
		//log.Printf("DEBUG: Response body: %s", string(body))
	}

	// parse json
	var response DatastoreResponse
	err = json.Unmarshal(body, &response)
	if err != nil {
		return err
	}

	// for each datastore collect metrics
	for _, datastore := range response.Data {
		err := e.getDatastoreMetric(datastore, ch)
		if err != nil {
			return err
		}
	}

	// get node metrics
	err = e.getNodeMetrics(ch)
	if err != nil {
		return err
	}

	return nil
}

func (e *Exporter) getNodeMetrics(ch chan<- prometheus.Metric) error {
	// NOTE: According to the api documentation, we have to provide the node name (won't work with the node ip),
	// but it seems to work with any name, so we just use "localhost" here.
	// see: https://pbs.proxmox.com/docs/api-viewer/index.html#/nodes/{node}
	req, err := http.NewRequest("GET", e.endpoint+nodeApi+"/localhost/status", nil)
	if err != nil {
		return err
	}

	// add Authorization header
	req.Header.Set("Authorization", e.authorizationHeader)

	// debug
	if *loglevel == "debug" {
		log.Printf("DEBUG: Request URL: %s", req.URL)
		log.Printf("DEBUG: Request Header: %s", req.Header)
	}

	// make request and show output
	resp, err := client.Do(req)
	if err != nil {
		return err
	}

	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return err
	}

	// check if status code is 200
	if resp.StatusCode != 200 {
		return fmt.Errorf("ERROR: Status code %d returned from endpoint: %s", resp.StatusCode, e.endpoint)
	}

	// debug
	if *loglevel == "debug" {
		log.Printf("DEBUG: Status code %d returned from endpoint: %s", resp.StatusCode, e.endpoint)
		//log.Printf("DEBUG: Response body: %s", string(body))
	}

	// parse json
	var response HostResponse
	err = json.Unmarshal(body, &response)
	if err != nil {
		return err
	}

	// set host metrics
	ch <- prometheus.MustNewConstMetric(
		host_cpu_usage, prometheus.GaugeValue, float64(response.Data.CPU),
	)
	ch <- prometheus.MustNewConstMetric(
		host_memory_free, prometheus.GaugeValue, float64(response.Data.Mem.Free),
	)
	ch <- prometheus.MustNewConstMetric(
		host_memory_total, prometheus.GaugeValue, float64(response.Data.Mem.Total),
	)
	ch <- prometheus.MustNewConstMetric(
		host_memory_used, prometheus.GaugeValue, float64(response.Data.Mem.Used),
	)
	ch <- prometheus.MustNewConstMetric(
		host_swap_free, prometheus.GaugeValue, float64(response.Data.Swap.Free),
	)
	ch <- prometheus.MustNewConstMetric(
		host_swap_total, prometheus.GaugeValue, float64(response.Data.Swap.Total),
	)
	ch <- prometheus.MustNewConstMetric(
		host_swap_used, prometheus.GaugeValue, float64(response.Data.Swap.Used),
	)
	ch <- prometheus.MustNewConstMetric(
		host_disk_available, prometheus.GaugeValue, float64(response.Data.Disk.Avail),
	)
	ch <- prometheus.MustNewConstMetric(
		host_disk_total, prometheus.GaugeValue, float64(response.Data.Disk.Total),
	)
	ch <- prometheus.MustNewConstMetric(
		host_disk_used, prometheus.GaugeValue, float64(response.Data.Disk.Used),
	)
	ch <- prometheus.MustNewConstMetric(
		host_uptime, prometheus.GaugeValue, float64(response.Data.Uptime),
	)
	ch <- prometheus.MustNewConstMetric(
		host_io_wait, prometheus.GaugeValue, float64(response.Data.Wait),
	)

	return nil
}

func (e *Exporter) getDatastoreMetric(datastore Datastore, ch chan<- prometheus.Metric) error {
	// debug
	if *loglevel == "debug" {
		log.Printf("DEBUG: --Store %s", datastore.Store)
		log.Printf("DEBUG: --Avail %d", datastore.Avail)
		log.Printf("DEBUG: --Total %d", datastore.Total)
		log.Printf("DEBUG: --Used %d", datastore.Used)
	}

	// set datastore metrics
	ch <- prometheus.MustNewConstMetric(
		available, prometheus.GaugeValue, float64(datastore.Avail),
	)
	ch <- prometheus.MustNewConstMetric(
		size, prometheus.GaugeValue, float64(datastore.Total),
	)
	ch <- prometheus.MustNewConstMetric(
		used, prometheus.GaugeValue, float64(datastore.Used),
	)

	// get namespaces of datastore
	req, err := http.NewRequest("GET", e.endpoint+datastoreApi+"/"+datastore.Store+"/namespace", nil)
	if err != nil {
		return err
	}

	// add Authorization header
	req.Header.Set("Authorization", e.authorizationHeader)

	// debug
	if *loglevel == "debug" {
		log.Printf("DEBUG: --Request URL: %s", req.URL)
		log.Printf("DEBUG: --Request Header: %s", req.Header)
	}

	// make request and show output
	resp, err := client.Do(req)
	if err != nil {
		return err
	}

	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return err
	}

	// check if status code is 200
	if resp.StatusCode != 200 {
		return fmt.Errorf("ERROR: --Status code %d returned from endpoint: %s", resp.StatusCode, e.endpoint)
	}

	// debug
	if *loglevel == "debug" {
		log.Printf("DEBUG: --Status code %d returned from endpoint: %s", resp.StatusCode, e.endpoint)
		//log.Printf("DEBUG: Response body: %s", string(body))
	}

	// parse json
	var response NamespaceResponse
	err = json.Unmarshal(body, &response)
	if err != nil {
		return err
	}

	// for each namespace collect metrics
	for _, namespace := range response.Data {
		// if namespace is empty skip
		if namespace.Namespace == "" {
			continue
		}

		err := e.getNamespaceMetric(datastore.Store, namespace.Namespace, ch)
		if err != nil {
			return err
		}
	}

	return nil
}

func (e *Exporter) getNamespaceMetric(datastore string, namespace string, ch chan<- prometheus.Metric) error {
	// debug
	if *loglevel == "debug" {
		log.Printf("DEBUG: ----Namespace %s", namespace)
	}

	// get snapshots of datastore
	req, err := http.NewRequest("GET", e.endpoint+datastoreApi+"/"+datastore+"/snapshots?ns="+namespace, nil)
	if err != nil {
		return err
	}

	// add Authorization header
	req.Header.Set("Authorization", e.authorizationHeader)

	// debug
	if *loglevel == "debug" {
		log.Printf("DEBUG: ----Request URL: %s", req.URL)
		log.Printf("DEBUG: ----Request Header: %s", req.Header)
	}

	// make request and show output
	resp, err := client.Do(req)
	if err != nil {
		return err
	}

	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return err
	}

	// check if status code is 200
	if resp.StatusCode != 200 {
		return fmt.Errorf("ERROR: ----Status code %d returned from endpoint: %s", resp.StatusCode, e.endpoint)
	}

	// debug
	if *loglevel == "debug" {
		log.Printf("DEBUG: ----Status code %d returned from endpoint: %s", resp.StatusCode, e.endpoint)
		//log.Printf("DEBUG: Response body: %s", string(body))
	}

	// parse json
	var response SnapshotResponse
	err = json.Unmarshal(body, &response)
	if err != nil {
		return err
	}

	// set total snapshot metrics
	ch <- prometheus.MustNewConstMetric(
		snapshot_count, prometheus.GaugeValue, float64(len(response.Data)), namespace,
	)

	// set snapshot metrics per vm
	vmCount := make(map[string]int)
	for _, snapshot := range response.Data {
		// get vm name from snapshot
		vmName := snapshot.BackupID
		vmCount[vmName]++
	}

	// set snapshot metrics per vm
	for vmName, count := range vmCount {
		ch <- prometheus.MustNewConstMetric(
			snapshot_vm_count, prometheus.GaugeValue, float64(count), namespace, vmName,
		)
	}

	return nil
}

func main() {
	flag.Parse()

	// if env variable is set, it will overwrite defaults or flags
	if os.Getenv("PBS_LOGLEVEL") != "" {
		*loglevel = os.Getenv("PBS_LOGLEVEL")
	}
	if os.Getenv("PBS_ENDPOINT") != "" {
		*endpoint = os.Getenv("PBS_ENDPOINT")
	}
	if os.Getenv("PBS_USERNAME") != "" {
		*username = os.Getenv("PBS_USERNAME")
	}
	if os.Getenv("PBS_API_TOKEN_NAME") != "" {
		*apitokenname = os.Getenv("PBS_API_TOKEN_NAME")
	}
	if os.Getenv("PBS_API_TOKEN") != "" {
		*apitoken = os.Getenv("PBS_API_TOKEN")
	}
	if os.Getenv("PBS_TIMEOUT") != "" {
		*timeout = os.Getenv("PBS_TIMEOUT")
	}
	if os.Getenv("PBS_INSECURE") != "" {
		*insecure = os.Getenv("PBS_INSECURE")
	}
	if os.Getenv("PBS_METRICS_PATH") != "" {
		*metricsPath = os.Getenv("PBS_METRICS_PATH")
	}
	if os.Getenv("PBS_LISTEN_ADDRESS") != "" {
		*listenAddress = os.Getenv("PBS_LISTEN_ADDRESS")
	}

	// convert flags
	insecureBool, err := strconv.ParseBool(*insecure)
	if err != nil {
		log.Fatalf("ERROR: Unable to parse insecure: %s", err)
	}

	// set insecure
	if insecureBool {
		tr.TLSClientConfig.InsecureSkipVerify = true
	}

	// set timeout
	timeoutDuration, err := time.ParseDuration(*timeout)
	if err != nil {
		log.Fatalf("ERROR: Unable to parse timeout: %s", err)
	}
	client.Timeout = timeoutDuration

	// debug
	if *loglevel == "debug" {
		log.Printf("DEBUG: Using connection endpoint: %s", *endpoint)
		log.Printf("DEBUG: Using connection username: %s", *username)
		log.Printf("DEBUG: Using connection apitoken: %s", *apitoken)
		log.Printf("DEBUG: Using connection apitokenname: %s", *apitokenname)
		log.Printf("DEBUG: Using connection timeout: %s", client.Timeout)
		log.Printf("DEBUG: Using connection insecure: %t", tr.TLSClientConfig.InsecureSkipVerify)
		log.Printf("DEBUG: Using metrics path: %s", *metricsPath)
		log.Printf("DEBUG: Using listen address: %s", *listenAddress)
	}

	// register exporter
	exporter := NewExporter(*endpoint, *username, *apitoken, *apitokenname)
	prometheus.MustRegister(exporter)
	log.Printf("INFO: Using connection endpoint: %s", *endpoint)
	log.Printf("INFO: Listening on: %s", *listenAddress)
	log.Printf("INFO: Metrics path: %s", *metricsPath)

	// start http server
	http.Handle(*metricsPath, promhttp.Handler())
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html>
            <head><title>PBS Exporter</title></head>
            <body>
            <h1>Proxmox Backup Server Exporter</h1>
            <p><a href='` + *metricsPath + `'>Metrics</a></p>
            </body>
            </html>`))
	})
	log.Fatal(http.ListenAndServe(*listenAddress, nil))
}