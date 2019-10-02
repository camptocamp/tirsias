package main

import (
	"fmt"
	"os"
	"strings"

	promOperatorV1 "github.com/coreos/prometheus-operator/pkg/apis/monitoring/v1"
	promOperatorK8S "github.com/coreos/prometheus-operator/pkg/client/versioned"
	"github.com/grafana-tools/sdk"
	"github.com/jessevdk/go-flags"
	log "github.com/sirupsen/logrus"
	//apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var opts struct {
	Version                 bool   `short:"V" long:"version" description:"Display version."`
	KubeConfig              string `long:"kubeconfig" description:"Path to your kubeconfig file." env:"KUBECONFIG"`
	ClusterName             string `long:"cluster-name" description:"Name of the Kubernetes cluster." env:"CLUSTER_NAME"`
	ServiceAccountName      string `long:"service-account-name" description:"Service account name Grafana should use." env:"SERVICE_ACCOUNT_NAME"`
	ServiceAccountNamespace string `long:"service-account-namespace" description:"Service account namespace Grafana should use." env:"SERVICE_ACCOUNT_NAMESPACE"`
	Grafana                 struct {
		URL   string `long:"grafana-url" description:"Address of your Grafana instance." env:"GRAFANA_URL"`
		Token string `long:"grafana-token" description:"Authentication token for the Grafana instance." env:"GRAFANA_TOKEN"`
	} `group:"Grafana instance options"`
}

var (
	version    = "<<< filled in build time >>>"
	buildDate  = "<<< filled in build time >>>"
	commitSha1 = "<<< filled in build time >>>"
)

func main() {
	parser := flags.NewParser(&opts, flags.Default)
	_, err := parser.Parse()
	if flagsErr, ok := err.(*flags.Error); ok && flagsErr.Type == flags.ErrHelp {
		os.Exit(0)
	}
	if err != nil {
		log.Fatal(err)
	}

	if opts.Version {
		fmt.Printf("Tirsias v%s-%s (%s)\n", version, commitSha1, buildDate)
		os.Exit(0)
	}

	kubeConfig, err := getKubeConfig(opts.KubeConfig)
	if err != nil {
		log.Fatalf("failed to retrievez Kubernetes config: %s", err)
	}

	kubePromClient, err := promOperatorK8S.NewForConfig(kubeConfig)
	if err != nil {
		log.Fatalf("failed to create a Prometheus Operator client: %s", err)
	}

	kubeClient, err := kubernetes.NewForConfig(kubeConfig)
	if err != nil {
		log.Fatalf("failed to create Kubernetes client: %s", err)
	}

	sa, err := kubeClient.CoreV1().ServiceAccounts(opts.ServiceAccountNamespace).Get(opts.ServiceAccountName, metav1.GetOptions{})
	if err != nil {
		log.Fatalf("failed to retrieve service account: %s", err)
	}

	var promSecret string
	for _, s := range sa.Secrets {
		if strings.Contains(s.Name, "token") {
			promSecret = s.Name
			break
		}
	}
	secret, err := kubeClient.CoreV1().Secrets(opts.ServiceAccountNamespace).Get(promSecret, metav1.GetOptions{})
	if err != nil {
		log.Fatalf("failed to retrieve secret: %s", err)
	}

	prometheusToken := string(secret.Data["token"])

	// Verify Grafana connection by listing datasources
	grafanaClient := sdk.NewClient(opts.Grafana.URL, opts.Grafana.Token, sdk.DefaultHTTPClient)
	_, err = grafanaClient.GetAllDatasources()
	if err != nil {
		log.Fatalf("failed to verify Grafana datasources: %s", err)
	}

	watcher, err := kubePromClient.MonitoringV1().Prometheuses("").Watch(metav1.ListOptions{})
	if err != nil {
		log.Fatalf("failed to watch pods: %s", err)
	}
	ch := watcher.ResultChan()

	log.Infoln("Watching prometheuses...")

	for event := range ch {
		prometheus, ok := event.Object.(*promOperatorV1.Prometheus)
		if !ok {
			log.Fatalf("unexpected type")
		}
		generatedDS := sdk.Datasource{
			Name:   fmt.Sprintf("%s:%s:%s", opts.ClusterName, prometheus.GetNamespace(), prometheus.GetName()),
			Type:   "prometheus",
			Access: "proxy",
			URL:    fmt.Sprintf("%s/api/v1/namespaces/%s/services/%s:9090/proxy/", kubeConfig.Host, prometheus.GetNamespace(), prometheus.GetName()),
			JSONData: map[string]string{
				"httpMethod":      "GET",
				"httpHeaderName1": "Authorization",
			},
			SecureJSONData: map[string]string{
				"httpHeaderValue1": fmt.Sprintf("Bearer %s", prometheusToken),
			},
		}

		switch event.Type {
		case watch.Added, watch.Modified:
			ds, err := grafanaClient.GetDatasourceByName(generatedDS.Name)
			if err != nil {
				_, err = grafanaClient.CreateDatasource(generatedDS)
				if err != nil {
					log.Errorf("failed to create datasource: %s", err)
				}
				log.Infof("Datasource `%s' created.\n", generatedDS.Name)
				break
			}

			generatedDS.ID = ds.ID
			_, err = grafanaClient.UpdateDatasource(generatedDS)
			if err != nil {
				log.Errorf("failed to update datasource: %s", err)
			}
			/*
				if ds.Type != generatedDS.Type || ds.Access != generatedDS.Access || ds.URL != generatedDS.URL {
					generatedDS.ID = ds.ID
					_, err := grafanaClient.UpdateDatasource(generatedDS)
					if err != nil {
						log.Errorf("failed to update datasource: %s", err)
					}
					log.Infof("Datasource `%s' updated.\n", generatedDS.Name)
				}
			*/
		case watch.Deleted:
			_, err := grafanaClient.DeleteDatasourceByName(generatedDS.Name)
			if err != nil {
				log.Errorf("failed to delete datasource: %s", err)
			}
			log.Infof("Datasource `%s' deleted.\n", generatedDS.Name)
		case watch.Error:
			log.Errorf("watcher error encountered with pod `%s'", "foo")
		}
	}
}

func getKubeConfig(kubeConfigPath string) (config *rest.Config, err error) {
	if kubeConfigPath != "" {
		config, err = clientcmd.BuildConfigFromFlags("", kubeConfigPath)
	} else {
		config, err = rest.InClusterConfig()
	}
	return
}
