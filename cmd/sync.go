package cmd

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"

	"github.com/cloudflare/cloudflare-go"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	corev1 "k8s.io/api/core/v1"
)

// syncCmd represents the sync command
var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Syncs Cloudflare DNS-records with Kubernetes nodes",
	Run: func(cmd *cobra.Command, args []string) {
		ips := getNodesIPs()
		log.Infof("Got nodes ips=%v", ips)
		api, err := cloudflare.New(viper.GetString("cf-api-key"), viper.GetString("cf-api-email"))
		if err != nil {
			log.Fatal(err)
		}

		for _, n := range viper.GetStringSlice("cf-zones") {
			log.Infof("Processing zone %s", n)
			id, err := api.ZoneIDByName(n)
			if err != nil {
				log.Fatal(err)
			}
			recs, err := api.DNSRecords(id, cloudflare.DNSRecord{})
			if err != nil {
				log.Fatal(err)
			}
			sync(api, n, id, recs, ips)
		}
	},
}

func sync(api *cloudflare.API, zoneName, zoneID string, recs []cloudflare.DNSRecord, ips []string) {
	prefix := viper.GetString("domain-name-prefix")
	suffix := viper.GetString("domain-name-suffix")
	dryRun := viper.GetBool("dry-run")
	deleted := []string{}
	for _, r := range recs {
		if !strings.HasPrefix(r.Name, prefix) {
			continue
		}
		found := false
		for _, ip := range ips {
			if r.Content == ip {
				found = true
			}
		}
		if !found {
			log.Infof("Remove record \"%s\"", r.Name)
			deleted = append(deleted, r.Name)
			if !dryRun {
				err := api.DeleteDNSRecord(zoneID, r.ID)
				if err != nil {
					log.Fatal(err)
				}
			}
		}
	}
	for _, ip := range ips {
		byteIP := net.ParseIP(ip)
		hexIP := fmt.Sprintf("%02x%02x%02x%02x", byteIP[12], byteIP[13], byteIP[14], byteIP[15])
		name := prefix + hexIP + suffix + zoneName

		found := false
		for _, r := range recs {
			del := false
			for _, d := range deleted {
				if d == r.Name {
					del = true
				}
			}
			if del {
				continue
			}
			if r.Name == name {
				found = true
				log.Infof("Record \"%s\" with ip %s already exists", name, ip)
			}
		}
		if !found {
			log.Infof("Add record \"%s\" with ip %s", name, ip)
			if !dryRun {
				_, err := api.CreateDNSRecord(zoneID, cloudflare.DNSRecord{
					Type:    "A",
					Name:    name,
					Content: ip,
					Proxied: false,
				})
				if err != nil {
					log.Fatal(err)
				}
			}

		}
	}

}

func getNodesIPs() []string {
	kubeconfig := filepath.Join(os.Getenv("HOME"), ".kube", "config")
	log.Infof("Checking local kubekinfig path=%s", kubeconfig)
	var config *rest.Config
	if _, err := os.Stat(kubeconfig); err == nil {
		log.WithField("kubeconfig", kubeconfig).Info("Loading config from file (local mode)")
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			log.Fatal(err)
		}
	} else {
		log.Info("Loading config from cluster (cluster mode)")
		config, err = rest.InClusterConfig()
		if err != nil {
			log.Fatal(err)
		}
	}
	cl, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Fatal(err)
	}
	opts := metav1.ListOptions{}
	if viper.GetString("k8s-label-selector") != "" {
		opts.LabelSelector = viper.GetString("k8s-label-selector")
	}

	nodes, err := cl.CoreV1().Nodes().List(opts)
	if err != nil {
		log.Fatal(err)
	}
	res := []string{}
	for _, n := range nodes.Items {
		for _, a := range n.Status.Addresses {
			if a.Type == corev1.NodeAddressType(viper.GetString("k8s-address-type")) {
				res = append(res, a.Address)
			}
		}
	}
	return res
}

func init() {
	rootCmd.AddCommand(syncCmd)

	syncCmd.Flags().String("domain-name-prefix", "", "Domain name prefix")
	syncCmd.Flags().String("domain-name-suffix", "", "Domain name suffix")
	syncCmd.Flags().Bool("dry-run", false, "Dry run")
	syncCmd.Flags().String("cf-api-key", "", "Cloudflare API Key")
	syncCmd.Flags().String("cf-api-email", "", "Cloudflare API Email")
	syncCmd.Flags().String("k8s-label-selector", "", "Kubernetes node label selector")
	syncCmd.Flags().String("k8s-address-type", "ExternalIP", "Kubernetes node address type")
	syncCmd.Flags().StringSlice("cf-zones", []string{}, "Cloudflare zones")

	cobra.MarkFlagRequired(syncCmd.Flags(), "cf-api-key")
	cobra.MarkFlagRequired(syncCmd.Flags(), "cf-api-email")
	cobra.MarkFlagRequired(syncCmd.Flags(), "domain-name-prefix")
	cobra.MarkFlagRequired(syncCmd.Flags(), "domain-name-suffix")
	cobra.MarkFlagRequired(syncCmd.Flags(), "cf-zones")
}
