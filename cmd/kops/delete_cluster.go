/*
Copyright 2016 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"fmt"

	"io"

	"github.com/golang/glog"
	"github.com/spf13/cobra"
	"k8s.io/kops/cmd/kops/util"
	api "k8s.io/kops/pkg/apis/kops"
	"k8s.io/kops/pkg/apis/kops/registry"
	"k8s.io/kops/pkg/kubeconfig"
	"k8s.io/kops/upup/pkg/fi"
	"k8s.io/kops/upup/pkg/fi/cloudup"
	"k8s.io/kops/upup/pkg/fi/cloudup/awsup"
	"k8s.io/kops/upup/pkg/kutil"
	"k8s.io/kops/util/pkg/tables"
	"k8s.io/kops/util/pkg/vfs"
)

type DeleteClusterOptions struct {
	Yes         bool
	Region      string
	External    bool
	Unregister  bool
	ClusterName string
}

func NewCmdDeleteCluster(f *util.Factory, out io.Writer) *cobra.Command {
	options := &DeleteClusterOptions{}

	cmd := &cobra.Command{
		Use:   "cluster CLUSTERNAME [--yes]",
		Short: "Delete cluster",
		Long:  `Deletes a k8s cluster.`,
		Run: func(cmd *cobra.Command, args []string) {
			err := rootCommand.ProcessArgs(args)
			if err != nil {
				exitWithError(err)
			}

			// Note _not_ ClusterName(); we only want the --name flag
			options.ClusterName = rootCommand.clusterName

			err = RunDeleteCluster(f, out, options)
			if err != nil {
				exitWithError(err)
			}
		},
	}

	cmd.Flags().BoolVarP(&options.Yes, "yes", "y", options.Yes, "Specify --yes to delete the cluster")
	cmd.Flags().BoolVar(&options.Unregister, "unregister", options.Unregister, "Don't delete cloud resources, just unregister the cluster")
	cmd.Flags().BoolVar(&options.External, "external", options.External, "Delete an external cluster")

	cmd.Flags().StringVar(&options.Region, "region", options.Region, "region")
	return cmd
}

type getter func(o interface{}) interface{}

func RunDeleteCluster(f *util.Factory, out io.Writer, options *DeleteClusterOptions) error {
	var configBase vfs.Path

	clusterName := options.ClusterName
	if clusterName == "" {
		return fmt.Errorf("--name is required (for safety)")
	}

	var cloud fi.Cloud
	var cluster *api.Cluster
	var err error

	if options.External {
		region := options.Region
		if region == "" {
			return fmt.Errorf("--region is required (when --external)")
		}

		tags := map[string]string{"KubernetesCluster": clusterName}
		cloud, err = awsup.NewAWSCloud(region, tags)
		if err != nil {
			return fmt.Errorf("error initializing AWS client: %v", err)
		}
	} else {
		cluster, err = GetCluster(f, clusterName)
		if err != nil {
			return err
		}

		configBase, err = registry.ConfigBase(cluster)
		if err != nil {
			return err
		}
	}

	wouldDeleteCloudResources := false

	if !options.Unregister {
		if cloud == nil {
			cloud, err = cloudup.BuildCloud(cluster)
			if err != nil {
				return err
			}
		}

		// Todo lets make this smart enough to detect the cloud and switch on the ClusterResources interface
		d := &kutil.AwsCluster{}
		d.ClusterName = clusterName
		d.Cloud = cloud

		resources, err := d.ListResources()
		if err != nil {
			return err
		}

		if len(resources) == 0 {
			fmt.Fprintf(out, "No cloud resources to delete\n")
		} else {
			wouldDeleteCloudResources = true

			t := &tables.Table{}
			t.AddColumn("TYPE", func(r *kutil.ResourceTracker) string {
				return r.Type
			})
			t.AddColumn("ID", func(r *kutil.ResourceTracker) string {
				return r.ID
			})
			t.AddColumn("NAME", func(r *kutil.ResourceTracker) string {
				return r.Name
			})
			var l []*kutil.ResourceTracker
			for _, v := range resources {
				l = append(l, v)
			}

			err := t.Render(l, out, "TYPE", "NAME", "ID")
			if err != nil {
				return err
			}

			if !options.Yes {
				return fmt.Errorf("Must specify --yes to delete")
			}

			fmt.Fprintf(out, "\n")

			err = d.DeleteResources(resources)
			if err != nil {
				return err
			}
		}
	}

	if !options.External {
		if !options.Yes {
			if wouldDeleteCloudResources {
				fmt.Fprintf(out, "\nMust specify --yes to delete cloud resources & unregister cluster\n")
			} else {
				fmt.Fprintf(out, "\nMust specify --yes to unregister the cluster\n")
			}
			return nil
		}
		err := registry.DeleteAllClusterState(configBase)
		if err != nil {
			return fmt.Errorf("error removing cluster from state store: %v", err)
		}
	}

	b := kubeconfig.NewKubeconfigBuilder()
	b.Context = clusterName
	err = b.DeleteKubeConfig()
	if err != nil {
		glog.Warningf("error removing kube config: %v", err)
	}

	fmt.Fprintf(out, "\nCluster deleted\n")
	return nil
}
