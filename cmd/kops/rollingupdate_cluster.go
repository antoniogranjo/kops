package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"
	"k8s.io/kops/upup/pkg/fi/cloudup"
	"k8s.io/kops/upup/pkg/kutil"
	"k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/api/v1"
	"k8s.io/kubernetes/pkg/client/clientset_generated/release_1_3"
	"k8s.io/kubernetes/pkg/client/unversioned/clientcmd"
)

type RollingUpdateClusterCmd struct {
	Yes   bool
	Force bool

	cobraCommand *cobra.Command
}

var rollingupdateCluster = RollingUpdateClusterCmd{
	cobraCommand: &cobra.Command{
		Use:   "cluster",
		Short: "rolling-update cluster",
		Long:  `rolling-updates a k8s cluster.`,
	},
}

func init() {
	cmd := rollingupdateCluster.cobraCommand
	rollingUpdateCommand.cobraCommand.AddCommand(cmd)

	cmd.Flags().BoolVar(&rollingupdateCluster.Yes, "yes", false, "perform rolling update without confirmation")
	cmd.Flags().BoolVar(&rollingupdateCluster.Force, "force", false, "Force rolling update, even if no changes")

	cmd.Run = func(cmd *cobra.Command, args []string) {
		err := rollingupdateCluster.Run(args)
		if err != nil {
			exitWithError(err)
		}
	}
}

func (c *RollingUpdateClusterCmd) Run(args []string) error {
	err := rootCommand.ProcessArgs(args)
	if err != nil {
		return err
	}

	_, cluster, err := rootCommand.Cluster()
	if err != nil {
		return err
	}

	contextName := cluster.Name
	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(),
		&clientcmd.ConfigOverrides{CurrentContext: contextName}).ClientConfig()
	if err != nil {
		return fmt.Errorf("cannot load kubecfg settings for %q: %v", contextName, err)
	}

	k8sClient, err := release_1_3.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("cannot build kube client for %q: %v", contextName, err)
	}

	nodes, err := k8sClient.Core().Nodes().List(api.ListOptions{})
	if err != nil {
		return fmt.Errorf("error listing nodes in cluster: %v", err)
	}

	instanceGroupRegistry, err := rootCommand.InstanceGroupRegistry()
	if err != nil {
		return err
	}

	instancegroups, err := instanceGroupRegistry.ReadAll()
	if err != nil {
		return err
	}

	cloud, err := cloudup.BuildCloud(cluster)
	if err != nil {
		return err
	}

	d := &kutil.RollingUpdateCluster{}
	d.Cloud = cloud

	warnUnmatched := true
	groups, err := kutil.FindCloudInstanceGroups(cloud, cluster, instancegroups, warnUnmatched, nodes.Items)
	if err != nil {
		return err
	}

	{
		t := &Table{}
		t.AddColumn("NAME", func(r *kutil.CloudInstanceGroup) string {
			return r.InstanceGroup.Name
		})
		t.AddColumn("STATUS", func(r *kutil.CloudInstanceGroup) string {
			return r.Status
		})
		t.AddColumn("NEEDUPDATE", func(r *kutil.CloudInstanceGroup) string {
			return strconv.Itoa(len(r.NeedUpdate))
		})
		t.AddColumn("READY", func(r *kutil.CloudInstanceGroup) string {
			return strconv.Itoa(len(r.Ready))
		})
		t.AddColumn("MIN", func(r *kutil.CloudInstanceGroup) string {
			return strconv.Itoa(r.MinSize())
		})
		t.AddColumn("MAX", func(r *kutil.CloudInstanceGroup) string {
			return strconv.Itoa(r.MaxSize())
		})
		t.AddColumn("NODES", func(r *kutil.CloudInstanceGroup) string {
			var nodes []*v1.Node
			for _, i := range r.Ready {
				if i.Node != nil {
					nodes = append(nodes, i.Node)
				}
			}
			for _, i := range r.NeedUpdate {
				if i.Node != nil {
					nodes = append(nodes, i.Node)
				}
			}
			return strconv.Itoa(len(nodes))
		})
		var l []*kutil.CloudInstanceGroup
		for _, v := range groups {
			l = append(l, v)
		}

		err := t.Render(l, os.Stdout, "NAME", "STATUS", "NEEDUPDATE", "READY", "MIN", "MAX", "NODES")
		if err != nil {
			return err
		}
	}

	needUpdate := false
	for _, group := range groups {
		if len(group.NeedUpdate) != 0 {
			needUpdate = true
		}
	}

	if !needUpdate && !c.Force {
		fmt.Printf("\nNo rolling-update required\n")
		return nil
	}

	if !c.Yes {
		fmt.Printf("\nMust specify --yes to rolling-update\n")
		return nil
	}

	return d.RollingUpdate(groups, c.Force, k8sClient)
}
