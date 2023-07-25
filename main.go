package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/kelseyhightower/envconfig"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	humanize "github.com/dustin/go-humanize"
	log "github.com/sirupsen/logrus"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type Config struct {
	DefaultPath  string `envconfig:"DEFAULT_PATH" required:"true"`
	DelaySeconds int    `envconfig:"DELAY_SECONDS" default:"30"`
	StorageClass string `envconfig:"STORAGE_CLASS" default:"local-path"`
	NodeName     string `envconfig:"NODE_NAME" required:"true"`
}

type Metrics struct {
	pvcRequestedBytes *prometheus.GaugeVec
	pvcUsedBytes      *prometheus.GaugeVec
}

func main() {
	var config Config

	err := envconfig.Process("", &config)
	if err != nil {
		log.Fatal(err)
	}

	log.Info("path: ", config.DefaultPath, ", delay: ", config.DelaySeconds, ", storage class: ", config.StorageClass, ", node name: ", config.NodeName)

	ctx := context.Background()

	clientsetConfig, err := rest.InClusterConfig()
	if err != nil {
		log.Fatal(err)
	}

	clientset, err := kubernetes.NewForConfig(clientsetConfig)
	if err != nil {
		log.Fatal(err)
	}

	reg := prometheus.NewRegistry()
	metrics := NewMetrics(reg)

	go RecordMetrics(ctx, &config, clientset, metrics)

	http.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{Registry: reg}))
	http.ListenAndServe(":2112", nil)
}

func NewMetrics(reg prometheus.Registerer) *Metrics {
	metrics := &Metrics{
		pvcRequestedBytes: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "pvc_requested_bytes",
				Help: "The number of bytes requested by pvc",
			},
			[]string{"pvcname", "namespace", "storageclass", "pvname"},
		),
		pvcUsedBytes: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "pvc_used_bytes",
				Help: "The number of bytes used by pvc",
			},
			[]string{"pvcname", "namespace", "storageclass", "pvname"},
		),
	}
	reg.MustRegister(metrics.pvcRequestedBytes)
	reg.MustRegister(metrics.pvcUsedBytes)
	return metrics
}

func RecordMetrics(ctx context.Context, config *Config, clientset *kubernetes.Clientset, metrics *Metrics) {
	pvcClient := clientset.CoreV1().PersistentVolumeClaims(apiv1.NamespaceAll)

	for {
		// list directories
		entries, err := os.ReadDir(config.DefaultPath)
		if err != nil {
			log.Error(err)
		}

		sizeMap := make(map[string]uint64)

		// calculate each directory size and write to size map
		for _, entry := range entries {
			size, err := DirSize(path.Join(config.DefaultPath, entry.Name()))
			if err != nil {
				log.Error(err)
			}
			sizeMap[entry.Name()] = size
		}

		// list pvc in all namespaces
		pvcList, err := pvcClient.List(ctx, metav1.ListOptions{})
		if err != nil {
			log.Error(err)
		}

		// for each pvc write metric if pvc found in size map
		for _, pvc := range pvcList.Items {
			if *pvc.Spec.StorageClassName != config.StorageClass {
				continue
			}
			key := fmt.Sprintf("%s_%s_%s", pvc.Spec.VolumeName, pvc.ObjectMeta.Namespace, pvc.ObjectMeta.Name)
			sizeUsedBytes, ok := sizeMap[key]
			if ok {
				sizeRequestedBytes, err := humanize.ParseBytes(pvc.Spec.Resources.Requests.Storage().String())
				if err != nil {
					log.Error(err)
				}

				metrics.pvcRequestedBytes.With(
					prometheus.Labels{
						"pvcname":      pvc.ObjectMeta.Name,
						"namespace":    pvc.ObjectMeta.Namespace,
						"storageclass": *pvc.Spec.StorageClassName,
						"pvname":       pvc.Spec.VolumeName,
					},
				).Set(float64(sizeRequestedBytes))

				metrics.pvcUsedBytes.With(
					prometheus.Labels{
						"pvcname":      pvc.ObjectMeta.Name,
						"namespace":    pvc.ObjectMeta.Namespace,
						"storageclass": *pvc.Spec.StorageClassName,
						"pvname":       pvc.Spec.VolumeName,
					},
				).Set(float64(sizeUsedBytes))
			}
		}

		// delay between calculations
		time.Sleep(time.Duration(config.DelaySeconds) * time.Second)
	}
}

func DirSize(path string) (uint64, error) {
	var size int64
	err := filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			size += info.Size()
		}
		return err
	})
	return uint64(size), err
}
