package cmd

import (
	"context"
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
		ctx := context.Background()
		cl := getClient()
		nodes := getNodes(ctx, cl)
		log.Infof("Got nodes ips=%v", nodes)
		api, err := cloudflare.New(viper.GetString("cf-api-key"), viper.GetString("cf-api-email"))
		if err != nil {
			log.Fatal(err)
		}

		for _, n := range viper.GetStringSlice("cf-zones") {
			log.Infof("Processing zone %s", n)
			zoneID, err := api.ZoneIDByName(n)
			if err != nil {
				log.Fatal(err)
			}
			recs, _, err := api.ListDNSRecords(ctx, cloudflare.ZoneIdentifier(zoneID), cloudflare.ListDNSRecordsParams{})
			if err != nil {
				log.Fatal(err)
			}
			sync(ctx, cl, api, n, zoneID, recs, nodes)
		}
	},
}

var updatedNodes = map[string]bool{}

func updateNodeLabel(ctx context.Context, cl *kubernetes.Clientset, n Node) {
	labelName := viper.GetString("k8s-label-name")
	if labelName == "" {
		return
	}
	if _, ok := updatedNodes[n.Name]; ok {
		return
	}
	kn, err := cl.CoreV1().Nodes().Get(ctx, n.Name, metav1.GetOptions{})
	if err != nil {
		log.Fatal(err)
	}
	if v, ok := kn.ObjectMeta.Labels[labelName]; !ok || v != n.Subdomain {
		log.Infof("Set label \"%s\" with value \"%s\" for node \"%s\"", labelName, n.Subdomain, n.Name)
		kn.ObjectMeta.Labels[labelName] = n.Subdomain
		_, err := cl.CoreV1().Nodes().Update(ctx, kn, metav1.UpdateOptions{})
		if err != nil {
			log.Fatal(err)
		}
		updatedNodes[n.Name] = true
	}
}

func makeSubdomainName(prefix string, ip string) string {
	byteIP := net.ParseIP(ip)
	hexIP := fmt.Sprintf("%02x%02x%02x%02x", byteIP[12], byteIP[13], byteIP[14], byteIP[15])
	return prefix + hexIP
}

func sync(ctx context.Context, cl *kubernetes.Clientset, api *cloudflare.API, zoneName, zoneID string, recs []cloudflare.DNSRecord, nodes []Node) {
	prefix := viper.GetString("domain-name-prefix")
	dryRun := viper.GetBool("dry-run")
	suffixes := strings.Split(viper.GetString("domain-name-suffix"), ",")
	var deleted []string
	for _, r := range recs {
		if !strings.HasPrefix(r.Name, prefix) {
			continue
		}
		found := false
		for _, n := range nodes {
			if r.Content == n.IP {
				found = true
			}
		}
		if !found {
			log.Infof("Remove record \"%s\"", r.Name)
			deleted = append(deleted, r.Name)
			if !dryRun {
				err := api.DeleteDNSRecord(ctx, cloudflare.ZoneIdentifier(zoneID), r.ID)
				if err != nil {
					log.Fatal(err)
				}
			}
		}
	}
	for _, n := range nodes {
		for _, suffix := range suffixes {
			suffix = strings.TrimSpace(suffix)

			name := n.Subdomain + suffix + zoneName

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
					log.Infof("Record \"%s\" with ip %s already exists", name, n.IP)
					updateNodeLabel(ctx, cl, n)
				}
			}
			if !found {
				log.Infof("Add record \"%s\" with ip %s", name, n.IP)
				if !dryRun {
					_, err := api.CreateDNSRecord(ctx, cloudflare.ZoneIdentifier(zoneID), cloudflare.CreateDNSRecordParams{
						Type:    "A",
						Name:    name,
						Content: n.IP,
						Proxied: cloudflare.BoolPtr(false),
					})
					if err != nil {
						log.Fatal(err)
					}
					updateNodeLabel(ctx, cl, n)
				}
			}
		}

	}
}

func getClient() *kubernetes.Clientset {
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
	return cl
}

type Node struct {
	Name      string
	IP        string
	Subdomain string
}

func getNodes(ctx context.Context, cl *kubernetes.Clientset) []Node {
	prefix := viper.GetString("domain-name-prefix")
	opts := metav1.ListOptions{}
	if viper.GetString("k8s-label-selector") != "" {
		opts.LabelSelector = viper.GetString("k8s-label-selector")
	}

	nodes, err := cl.CoreV1().Nodes().List(ctx, opts)
	if err != nil {
		log.Fatal(err)
	}
	res := []Node{}
	for _, n := range nodes.Items {
		for _, a := range n.Status.Addresses {
			if a.Type == corev1.NodeAddressType(viper.GetString("k8s-address-type")) {
				res = append(res, Node{
					Name:      n.Name,
					IP:        a.Address,
					Subdomain: makeSubdomainName(prefix, a.Address),
				})
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
	syncCmd.Flags().String("k8s-label-name", "", "Kubernetes node label name")
	syncCmd.Flags().String("k8s-address-type", "ExternalIP", "Kubernetes node address type")
	syncCmd.Flags().StringSlice("cf-zones", []string{}, "Cloudflare zones")

	cobra.MarkFlagRequired(syncCmd.Flags(), "cf-api-key")
	cobra.MarkFlagRequired(syncCmd.Flags(), "cf-api-email")
	cobra.MarkFlagRequired(syncCmd.Flags(), "domain-name-prefix")
	cobra.MarkFlagRequired(syncCmd.Flags(), "domain-name-suffix")
	cobra.MarkFlagRequired(syncCmd.Flags(), "cf-zones")
}
