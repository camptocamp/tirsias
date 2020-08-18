package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	promOperatorV1 "github.com/coreos/prometheus-operator/pkg/apis/monitoring/v1"
	promOperatorK8S "github.com/coreos/prometheus-operator/pkg/client/versioned"
	"github.com/grafana-tools/sdk"
	"github.com/jessevdk/go-flags"
	log "github.com/sirupsen/logrus"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
)

var opts struct {
	Version                 bool   `short:"V" long:"version" description:"Display version."`
	KubeConfig              string `long:"kubeconfig" description:"Path to your kubeconfig file." env:"KUBECONFIG"`
	ClusterName             string `long:"cluster-name" description:"Name of the Kubernetes cluster." env:"CLUSTER_NAME"`
	ServiceAccountName      string `long:"service-account-name" description:"Service account name Grafana should use." env:"SERVICE_ACCOUNT_NAME"`
	ServiceAccountNamespace string `long:"service-account-namespace" description:"Service account namespace Grafana should use." env:"SERVICE_ACCOUNT_NAMESPACE"`
	KubernetesPublicAddress string `long:"kubernetes-public-address" description:"Public address of the Kubernetes cluster." env:"KUBERNETES_PUBLIC_ADDRESS"`
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
	log.SetReportCaller(true)
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
	_, err = grafanaClient.GetAllDatasources(context.Background())
	if err != nil {
		log.Fatalf("failed to verify Grafana datasources: %s", err)
	}

	watchlist := cache.NewListWatchFromClient(
		kubePromClient.MonitoringV1().RESTClient(),
		string("prometheuses"),
		apiv1.NamespaceAll,
		fields.Everything(),
	)

	_, controller := cache.NewInformer(
		watchlist,
		&promOperatorV1.Prometheus{},
		0,
		cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				prometheus, ok := obj.(*promOperatorV1.Prometheus)
				if !ok {
					log.Errorf("unexpected type: %+v", obj)
				}

				createOrUpdateDatasource(
					grafanaClient,
					opts.ClusterName,
					opts.KubernetesPublicAddress,
					prometheusToken,
					prometheus,
				)
			},
			DeleteFunc: func(obj interface{}) {
				prometheus, ok := obj.(*promOperatorV1.Prometheus)
				if !ok {
					log.Errorf("unexpected type: %+v", obj)
				}
				ds := generateDatasource(
					opts.ClusterName,
					prometheus.GetNamespace(),
					prometheus.GetName(),
					opts.KubernetesPublicAddress,
					prometheusToken,
				)
				_, err := grafanaClient.DeleteDatasourceByName(context.Background(), ds.Name)
				if err != nil {
					log.Errorf("failed to delete datasource: %s", err)
				}
				log.Infof("Datasource `%s' deleted.\n", ds.Name)
			},
			UpdateFunc: func(oldObj, newObj interface{}) {
				prometheus, ok := newObj.(*promOperatorV1.Prometheus)
				if !ok {
					log.Errorf("unexpected type: %+v", newObj)
				}

				createOrUpdateDatasource(
					grafanaClient,
					opts.ClusterName,
					opts.KubernetesPublicAddress,
					prometheusToken,
					prometheus,
				)
			},
		},
	)

	stop := make(chan struct{})
	defer close(stop)
	go controller.Run(stop)

	log.Infoln("Watching prometheuses...")

	select {}
}

func getKubeConfig(kubeConfigPath string) (config *rest.Config, err error) {
	if kubeConfigPath != "" {
		config, err = clientcmd.BuildConfigFromFlags("", kubeConfigPath)
	} else {
		config, err = rest.InClusterConfig()
	}
	return
}

func generateStandardExternalURL(kubernetesPublicAddress, namespace, name string) string {
	return fmt.Sprintf("%s/api/v1/namespaces/%s/services/%s:9090/proxy/", kubernetesPublicAddress, namespace, name)
}

func generateDatasource(clusterName, namespace, name, externalURL, prometheusToken string) sdk.Datasource {
	return sdk.Datasource{
		Name:   fmt.Sprintf("%s:%s:%s", clusterName, namespace, name),
		Type:   "prometheus",
		Access: "proxy",
		URL:    externalURL,
		JSONData: map[string]string{
			"httpMethod":      "GET",
			"httpHeaderName1": "Authorization",
		},
		SecureJSONData: map[string]string{
			"httpHeaderValue1": fmt.Sprintf("Bearer %s", prometheusToken),
		},
	}
}

func createOrUpdateDatasource(grafanaClient *sdk.Client, clusterName, kubernetesPublicAddress, prometheusToken string, prometheus *promOperatorV1.Prometheus) {

	var externalURL string
	if prometheus.Spec.ExternalURL != "" {
		externalURL = prometheus.Spec.ExternalURL
	} else {
		externalURL = generateStandardExternalURL(kubernetesPublicAddress, prometheus.GetNamespace(), prometheus.GetName())
	}

	orgs, _ := grafanaClient.GetAllOrgs(context.Background())

	log.Warningf("%+v", orgs)

	ds := generateDatasource(
		clusterName,
		prometheus.GetNamespace(),
		prometheus.GetName(),
		externalURL,
		prometheusToken,
	)

	returnedDatasource, err := grafanaClient.GetDatasourceByName(context.Background(), ds.Name)
	if err != nil {
		_, err = grafanaClient.CreateDatasource(context.Background(), ds)
		if err != nil {
			log.Errorf("failed to create datasource: %s", err)
		}
		log.Infof("Datasource `%s' created.\n", ds.Name)
	} else {
		ds.ID = returnedDatasource.ID
		_, err = grafanaClient.UpdateDatasource(context.Background(), ds)
		if err != nil {
			log.Errorf("failed to update datasource: %s", err)
		}
		log.Infof("Datasource `%s' updated.\n", ds.Name)
	}
}
