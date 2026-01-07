# Tofu Controller: An IAC Controller for Flux

[![OpenSSF Best Practices](https://bestpractices.coreinfrastructure.org/projects/7761/badge)](https://bestpractices.coreinfrastructure.org/projects/7761)

Tofu Controller (previously known as Weave TF-Controller) is a controller for [Flux](https://fluxcd.io) to reconcile OpenTofu and Terraform resources
in the GitOps way.
With the power of Flux together with OpenTofu and Terraform, Tofu Controller allows you to GitOps-ify infrastructure,
and application resources, in the Kubernetes and IAC universe, at your own pace.

"At your own pace" means you don't need to GitOps-ify everything at once.

Tofu Controller offers many GitOps models:
  1. **GitOps Automation Model:** GitOps your OpenTofu and Terraform resources from the provision steps to the enforcement steps, like a whole EKS cluster.
  2. **Hybrid GitOps Automation Model:** GitOps parts of your existing infrastructure resources. For example, you have an existing EKS cluster.
     You can choose to GitOps only its nodegroup, or its security group.
  3. **State Enforcement Model:** You have a TFSTATE file, and you'd like to use GitOps enforce it, without changing anything else.
  4. **Drift Detection Model:** You have a TFSTATE file, and you'd like to use GitOps just for drift detection, so you can decide to do things later when a drift occurs.

# Fork Information

This is a personal fork of [flux-iac/tofu-controller](https://github.com/flux-iac/tofu-controller) maintained by João Leão.

**Original Project:** [flux-iac/tofu-controller](https://github.com/flux-iac/tofu-controller)

## Purpose

This fork adds **hybrid plan storage** capabilities to handle Terraform plans that exceed Kubernetes Secret size limits (1MB).

## Key Features

### Hybrid Storage Strategy

The controller automatically selects the optimal storage strategy based on plan size:

1. **Chunked Secrets (< 900KB)**: Plans split into multiple 1MB secret chunks
2. **Ephemeral Volumes (≥ 900KB)**: Plans stored in pod-local ephemeral volumes
3. **Legacy Single Secret**: Backward compatible with existing deployments

## Benefits

- ✅ Unlimited plan size via volume storage
- ✅ Backward compatible with existing deployments
- ✅ Efficient - small plans use secrets, large plans use volumes
- ✅ Auto-cleanup - no orphaned plan data
- ✅ Production tested in enterprise environments

## Installation

Refer to the [upstream documentation](https://github.com/flux-iac/tofu-controller) for installation instructions. This fork maintains compatibility with the standard installation process.

## Documentation

For comprehensive documentation, please visit the [upstream project documentation](https://flux-iac.github.io/tofu-controller/).

## License

Apache License 2.0

## Contact

- **LinkedIn:** [linkedin.com/in/joaoleao7](https://linkedin.com/in/joaoleao7)
- **Original Project:** [flux-iac/tofu-controller](https://github.com/flux-iac/tofu-controller)

