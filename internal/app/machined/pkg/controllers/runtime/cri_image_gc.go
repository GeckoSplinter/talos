// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package runtime

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/benbjohnson/clock"
	"github.com/containerd/containerd"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/namespaces"
	"github.com/cosi-project/runtime/pkg/controller"
	"github.com/cosi-project/runtime/pkg/resource"
	"github.com/cosi-project/runtime/pkg/safe"
	"github.com/cosi-project/runtime/pkg/state"
	"github.com/docker/distribution/reference"
	"github.com/siderolabs/gen/slices"
	"github.com/siderolabs/go-pointer"
	"go.uber.org/zap"

	"github.com/siderolabs/talos/pkg/machinery/constants"
	"github.com/siderolabs/talos/pkg/machinery/resources/etcd"
	"github.com/siderolabs/talos/pkg/machinery/resources/k8s"
	"github.com/siderolabs/talos/pkg/machinery/resources/v1alpha1"
)

// ImageCleanupInterval is the interval at which the image GC controller runs.
const ImageCleanupInterval = 15 * time.Minute

// ImageGCGracePeriod is the minimum age of an image before it can be deleted.
const ImageGCGracePeriod = 4 * ImageCleanupInterval

// CRIImageGCController renders manifests based on templates and config/secrets.
type CRIImageGCController struct {
	ImageServiceProvider func() (ImageServiceProvider, error)
	Clock                clock.Clock
}

// ImageServiceProvider wraps the containerd image service.
type ImageServiceProvider interface {
	ImageService() images.Store
	Close() error
}

// Name implements controller.Controller interface.
func (ctrl *CRIImageGCController) Name() string {
	return "runtime.CRIImageGCController"
}

// Inputs implements controller.Controller interface.
func (ctrl *CRIImageGCController) Inputs() []controller.Input {
	return []controller.Input{
		{
			Namespace: v1alpha1.NamespaceName,
			Type:      v1alpha1.ServiceType,
			ID:        pointer.To("cri"),
			Kind:      controller.InputWeak,
		},
		{
			Namespace: k8s.NamespaceName,
			Type:      k8s.KubeletSpecType,
			ID:        pointer.To(k8s.KubeletID),
			Kind:      controller.InputWeak,
		},
		{
			Namespace: etcd.NamespaceName,
			Type:      etcd.SpecType,
			ID:        pointer.To(etcd.SpecID),
			Kind:      controller.InputWeak,
		},
	}
}

// Outputs implements controller.Controller interface.
func (ctrl *CRIImageGCController) Outputs() []controller.Output {
	return nil
}

func defaultImageServiceProvider() (ImageServiceProvider, error) {
	criClient, err := containerd.New(constants.CRIContainerdAddress)
	if err != nil {
		return nil, fmt.Errorf("error creating CRI containerd client: %w", err)
	}

	return &containerdImageServiceProvider{
		criClient: criClient,
	}, nil
}

type containerdImageServiceProvider struct {
	criClient *containerd.Client
}

func (s *containerdImageServiceProvider) ImageService() images.Store {
	return s.criClient.ImageService()
}

func (s *containerdImageServiceProvider) Close() error {
	return s.criClient.Close()
}

// Run implements controller.Controller interface.
//
//nolint:gocyclo,cyclop
func (ctrl *CRIImageGCController) Run(ctx context.Context, r controller.Runtime, logger *zap.Logger) error {
	if ctrl.ImageServiceProvider == nil {
		ctrl.ImageServiceProvider = defaultImageServiceProvider
	}

	if ctrl.Clock == nil {
		ctrl.Clock = clock.New()
	}

	var (
		criIsUp              bool
		expectedImages       []string
		imageServiceProvider ImageServiceProvider
	)

	ticker := ctrl.Clock.Ticker(ImageCleanupInterval)
	defer ticker.Stop()

	defer func() {
		if imageServiceProvider != nil {
			imageServiceProvider.Close() //nolint:errcheck
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if !criIsUp || len(expectedImages) == 0 {
				continue
			}

			if imageServiceProvider == nil {
				var err error

				imageServiceProvider, err = ctrl.ImageServiceProvider()
				if err != nil {
					return fmt.Errorf("error creating image service provider: %w", err)
				}
			}

			if err := ctrl.cleanup(ctx, logger, imageServiceProvider.ImageService(), expectedImages); err != nil {
				return fmt.Errorf("error running image cleanup: %w", err)
			}
		case <-r.EventCh():
			criService, err := safe.ReaderGet[*v1alpha1.Service](ctx, r, resource.NewMetadata(v1alpha1.NamespaceName, v1alpha1.ServiceType, "cri", resource.VersionUndefined))
			if err != nil && !state.IsNotFoundError(err) {
				return fmt.Errorf("error getting CRI service: %w", err)
			}

			criIsUp = criService != nil && criService.TypedSpec().Running && criService.TypedSpec().Healthy

			expectedImages = nil

			etcdSpec, err := safe.ReaderGet[*etcd.Spec](ctx, r, resource.NewMetadata(etcd.NamespaceName, etcd.SpecType, etcd.SpecID, resource.VersionUndefined))
			if err != nil && !state.IsNotFoundError(err) {
				return fmt.Errorf("error getting etcd spec: %w", err)
			}

			if etcdSpec != nil {
				expectedImages = append(expectedImages, etcdSpec.TypedSpec().Image)
			}

			kubeletSpec, err := safe.ReaderGet[*k8s.KubeletSpec](ctx, r, resource.NewMetadata(k8s.NamespaceName, k8s.KubeletSpecType, k8s.KubeletID, resource.VersionUndefined))
			if err != nil && !state.IsNotFoundError(err) {
				return fmt.Errorf("error getting etcd spec: %w", err)
			}

			if kubeletSpec != nil {
				expectedImages = append(expectedImages, kubeletSpec.TypedSpec().Image)
			}
		}

		r.ResetRestartBackoff()
	}
}

//nolint:gocyclo
func (ctrl *CRIImageGCController) cleanup(ctx context.Context, logger *zap.Logger, imageService images.Store, expectedImages []string) error {
	logger.Info("running image cleanup")

	ctx = namespaces.WithNamespace(ctx, constants.SystemContainerdNamespace)

	actualImages, err := imageService.List(ctx)
	if err != nil {
		return fmt.Errorf("error listing images: %w", err)
	}

	var parseErrors []error

	expectedReferences := slices.Map(expectedImages, func(ref string) reference.Named {
		res, parseErr := reference.ParseNamed(ref)

		parseErrors = append(parseErrors, parseErr)

		return res
	})

	if err = errors.Join(parseErrors...); err != nil {
		return fmt.Errorf("error parsing expected images: %w", err)
	}

	for _, image := range actualImages {
		imageRef, err := reference.ParseNamed(image.Name)
		if err != nil {
			logger.Info("failed to parse image name", zap.String("image", image.Name), zap.Error(err))

			continue
		}

		shouldDelete := true

		for _, expectedRef := range expectedReferences {
			if imageRef.Name() != expectedRef.Name() {
				continue
			}

			imageTagged, ok1 := imageRef.(reference.Tagged)
			expectedTagged, ok2 := expectedRef.(reference.Tagged)

			if ok1 && ok2 {
				if imageTagged.Tag() == expectedTagged.Tag() {
					shouldDelete = false

					break
				}
			}

			imageDigested, ok1 := imageRef.(reference.Digested)
			expectedDigested, ok2 := expectedRef.(reference.Digested)

			if ok1 && ok2 {
				if imageDigested.Digest().Encoded() == expectedDigested.Digest().Encoded() {
					shouldDelete = false

					break
				}
			}
		}

		if !shouldDelete {
			logger.Debug("image is referenced, skipping garbage collection", zap.String("image", image.Name))

			continue
		}

		imageAge := ctrl.Clock.Since(image.CreatedAt)
		if imageAge < ImageGCGracePeriod {
			logger.Debug("skipping image cleanup, as it's below minimum age", zap.String("image", image.Name), zap.Duration("age", imageAge))

			continue
		}

		if err = imageService.Delete(ctx, image.Name); err != nil {
			return fmt.Errorf("failed to delete an image %s: %w", image.Name, err)
		}

		logger.Info("deleted an image", zap.String("image", image.Name))
	}

	return nil
}
