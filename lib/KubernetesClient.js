/* eslint-disable prettier/prettier */
import * as k8s from '@kubernetes/client-node';

class KubernetesClient {
    constructor(configManager) {
        this.configManager = configManager;
        
        // Initialize Kubernetes client
        this.kc = new k8s.KubeConfig();
        this.kc.loadFromCluster();
        this.k8sApi = this.kc.makeApiClient(k8s.CoreV1Api);
        this.customApi = this.kc.makeApiClient(k8s.CustomObjectsApi);
    }

    async getCurrentProfile() {
        try {
            const response = await this.customApi.getNamespacedCustomObject({
                group: 'rabbitmq.com',
                version: 'v1beta1',
                namespace: this.configManager.namespace,
                plural: 'rabbitmqclusters',
                name: this.configManager.rmqServiceName
            });

            const currentCpu = response.spec?.resources?.requests?.cpu || '0';
            
            return this.configManager.cpuToProfileMap[currentCpu] || 'UNKNOWN';
        } catch (error) {
            console.error('Error getting current profile:', error.message);
            return 'UNKNOWN';
        }
    }

    async getStabilityState() {
        console.log(`🔍 Getting stability state from ConfigMap: ${this.configManager.configMapName} in namespace: ${this.configManager.namespace}`);
        try {
            const response = await this.k8sApi.readNamespacedConfigMap({
                name: this.configManager.configMapName,
                namespace: this.configManager.namespace
            });

            return {
                stableProfile: response.data?.stable_profile || '',
                stableSince: parseInt(response.data?.stable_since || '0')
            };
        } catch (error) {
            console.error('Error getting stability state:', error.message);
            console.error('Full error details:', error);
            return { stableProfile: '', stableSince: 0 };
        }
    }

    async updateStabilityTracking(profile) {
        const currentTime = Math.floor(Date.now() / 1000);

        try {
            const patchOps = [
                {
                    op: 'replace',
                    path: '/data/stable_profile',
                    value: profile
                },
                {
                    op: 'replace',
                    path: '/data/stable_since',
                    value: currentTime.toString()
                }
            ];

            await this.k8sApi.patchNamespacedConfigMap({
                name: this.configManager.configMapName,
                namespace: this.configManager.namespace,
                body: patchOps
            }, k8s.setHeaderOptions('Content-Type', k8s.PatchStrategy.JsonPatch));

            console.log(`📝 Updated stability tracking: ${profile} since ${currentTime}`);
        } catch (error) {
            console.error('Error updating stability tracking:', error.message);
        }
    }

    async applyPatch(resources) {
        try {
            // Apply the patch to RabbitMQ cluster
            const patchOps = [
                {
                    op: 'replace',
                    path: '/spec/resources/requests/cpu',
                    value: resources.cpu
                },
                {
                    op: 'replace',
                    path: '/spec/resources/requests/memory',
                    value: resources.memory
                }
            ];

            await this.customApi.patchNamespacedCustomObject({
                group: 'rabbitmq.com',
                version: 'v1beta1',
                namespace: this.configManager.namespace,
                plural: 'rabbitmqclusters',
                name: this.configManager.rmqServiceName,
                body: patchOps
            }, k8s.setHeaderOptions('Content-Type', k8s.PatchStrategy.JsonPatch));

            console.log('✅ Scaling completed successfully');
        } catch (error) {
            console.error('❌ Scaling failed:', error.message);
            throw error;
        }
    }
}

export default KubernetesClient;