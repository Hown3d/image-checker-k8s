package k8s

import (
	"context"
	"errors"
	"strings"

	log "github.com/sirupsen/logrus"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (k *KubernetesConfig) GetRessourcesToUpdate(listOpts *metav1.ListOptions) error {
	namespaces, err := k.getNamespaces()

	if err != nil {
		return err
	}
	for _, namespace := range namespaces {
		log.Infof("Checking Namespace %v", namespace)
		pods, err := k.KubeClient.CoreV1().Pods(namespace).List(context.Background(), *listOpts)
		if err != nil {
			return err
		}
		for _, pod := range pods.Items {
			podOwnerData, err := k.getPodOwner(namespace, &pod)
			if err != nil {
				log.Errorf("Pod Owner couldn't be fetched: %v", err)
				continue
			}
			log.Infof("Checking pod %v", pod.Name)
			k.updateSetIfNeeded(podOwnerData)
		}

	}
	return nil
}

func (k *KubernetesConfig) getPodOwner(namespace string, pod *apiv1.Pod) (*podOwnerMetaData, error) {
	ctx := context.Background()
	getOpts := metav1.GetOptions{}
	appsClient := k.KubeClient.AppsV1()
	for _, owner := range pod.OwnerReferences {
		switch owner.Kind {
		case "ReplicaSet":
			replicaSet, err := appsClient.ReplicaSets(namespace).Get(ctx, owner.Name, getOpts)
			if err != nil {
				return nil, err
			}
			// dont know why this is a for loop, idk if a pod can have more then 1 owner
			for _, rsOwner := range replicaSet.OwnerReferences {
				return &podOwnerMetaData{pod, rsOwner.Name, rsOwner.Kind}, nil
			}
		case "DaemonSet":
			daemonSet, err := appsClient.DaemonSets(namespace).Get(ctx, owner.Name, getOpts)
			if err != nil {
				return nil, err
			}
			return &podOwnerMetaData{pod, daemonSet.Name, daemonSet.Kind}, nil
		case "StatefulSet":
			statefulSet, err := appsClient.StatefulSets(namespace).Get(ctx, owner.Name, getOpts)
			if err != nil {
				return nil, err
			}
			return &podOwnerMetaData{pod, statefulSet.Name, statefulSet.Name}, nil
		default:
			return nil, errors.New("Can't update Pod with owner: " + owner.Kind)

		}
	}
	return nil, nil
}

func (k *KubernetesConfig) updateTemplateSpec(podTemplate *apiv1.PodTemplateSpec, newImage string) {
	for index := range podTemplate.Spec.Containers {
		//Use index instead of second value, because that returns a copy, not a reference
		podTemplate.Spec.Containers[index].Image = newImage
	}
}

func (k *KubernetesConfig) updateResource(newImage string, podOwnerMeta *podOwnerMetaData) error {
	ctx := context.Background()
	getOpts := metav1.GetOptions{}
	updateOpts := metav1.UpdateOptions{}
	switch podOwnerMeta.kind {
	case "Deployment":
		deploymentClient := k.KubeClient.AppsV1().Deployments(podOwnerMeta.pod.Namespace)
		deployment, err := deploymentClient.Get(ctx, podOwnerMeta.ownerName, getOpts)
		log.Infof("Updating Deployment %v", deployment.Name)
		if err != nil {
			return err
		}
		k.updateTemplateSpec(&deployment.Spec.Template, newImage)
		_, err = deploymentClient.Update(ctx, deployment, updateOpts)
		return err

		//case "DaemonSet":

	}
	return nil
}

func (k *KubernetesConfig) updateSetIfNeeded(podOwnerMeta *podOwnerMetaData) error {
	containerStatus := podOwnerMeta.pod.Status.ContainerStatuses
	containers := podOwnerMeta.pod.Spec.Containers

	for index := range containers {
		currentContainerStatus := containerStatus[index]
		currentContainer := containers[index]

		if k.RegistryOpts.IsNewImage(currentContainer.Image, currentContainerStatus.ImageID) {
			log.Infof("Container %v needs update, new digest found for image %v", currentContainer.Name, currentContainerStatus.Image)
			_, registryDigest := k.RegistryOpts.GetDigests(currentContainer.Image, currentContainerStatus.ImageID)

			// Image might already contain sha, because of previous update
			newImage := strings.Split(currentContainer.Image, "@")[0] + "@" + registryDigest
			log.Infof("New Image is %v", newImage)
			return k.updateResource(newImage, podOwnerMeta)
		}

	}
	return nil
}