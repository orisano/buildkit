// +build linux,!no_oci_worker

package main

import (
	"os/exec"

	ctdsnapshot "github.com/containerd/containerd/snapshots"
	"github.com/containerd/containerd/snapshots/native"
	"github.com/containerd/containerd/snapshots/overlay"
	"github.com/moby/buildkit/worker"
	"github.com/moby/buildkit/worker/base"
	"github.com/moby/buildkit/worker/runc"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
)

func init() {
	flags := []cli.Flag{
		cli.StringFlag{
			Name:  "oci-worker",
			Usage: "enable oci workers (true/false/auto)",
			Value: "auto",
		},
		cli.StringSliceFlag{
			Name:  "oci-worker-labels",
			Usage: "user-specific annotation labels (com.example.foo=bar)",
		},
		cli.StringFlag{
			Name:  "oci-worker-snapshotter",
			Usage: "name of snapshotter (overlayfs or native)",
			Value: "auto",
		},
	}
	n := "oci-worker-rootless"
	u := "enable rootless mode"
	if runningAsUnprivilegedUser() {
		flags = append(flags, cli.BoolTFlag{
			Name:  n,
			Usage: u,
		})
	} else {
		flags = append(flags, cli.BoolFlag{
			Name:  n,
			Usage: u,
		})
	}
	registerWorkerInitializer(
		workerInitializer{
			fn:       ociWorkerInitializer,
			priority: 0,
		},
		flags...,
	)
	// TODO: allow multiple oci runtimes
}

func ociWorkerInitializer(c *cli.Context, common workerInitializerOpt) ([]worker.Worker, error) {
	boolOrAuto, err := parseBoolOrAuto(c.GlobalString("oci-worker"))
	if err != nil {
		return nil, err
	}
	if (boolOrAuto == nil && !validOCIBinary()) || (boolOrAuto != nil && !*boolOrAuto) {
		return nil, nil
	}
	labels, err := attrMap(c.GlobalStringSlice("oci-worker-labels"))
	if err != nil {
		return nil, err
	}
	snFactory, err := snapshotterFactory(c.GlobalString("oci-worker-snapshotter"))
	if err != nil {
		return nil, err
	}
	// GlobalBool works for BoolT as well
	rootless := c.GlobalBool("oci-worker-rootless") || c.GlobalBool("rootless")
	if rootless {
		logrus.Debugf("running in rootless mode")
	}
	opt, err := runc.NewWorkerOpt(common.root, snFactory, rootless, labels)
	if err != nil {
		return nil, err
	}
	opt.SessionManager = common.sessionManager
	w, err := base.NewWorker(opt)
	if err != nil {
		return nil, err
	}
	return []worker.Worker{w}, nil
}

func snapshotterFactory(name string) (runc.SnapshotterFactory, error) {
	snFactory := runc.SnapshotterFactory{
		Name: name,
	}
	var err error
	switch name {
	case "auto":
		snFactory.New = func(root string) (ctdsnapshot.Snapshotter, error) {
			err := overlay.Supported(root)
			if err == nil {
				logrus.Debug("auto snapshotter: using overlayfs")
				return overlay.NewSnapshotter(root)
			}
			logrus.Debugf("auto snapshotter: using native for %s: %v", root, err)
			return native.NewSnapshotter(root)
		}
	case "native":
		snFactory.New = native.NewSnapshotter
	case "overlayfs": // not "overlay", for consistency with containerd snapshotter plugin ID.
		snFactory.New = func(root string) (ctdsnapshot.Snapshotter, error) {
			return overlay.NewSnapshotter(root)
		}
	default:
		err = errors.Errorf("unknown snapshotter name: %q", name)
	}
	return snFactory, err
}

func validOCIBinary() bool {
	_, err := exec.LookPath("runc")
	if err != nil {
		logrus.Warnf("skipping oci worker, as runc does not exist")
		return false
	}
	return true
}
