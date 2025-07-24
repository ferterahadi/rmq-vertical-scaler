import * as k8s from '@kubernetes/client-node';

export default class KubernetesClient {
  constructor(config) {
    this.namespace = config.namespace;
    this.rmqServiceName = config.rmqServiceName;
    this.configMapName = config.configMapName;
    
    this.kc = new k8s.KubeConfig();
    this.kc.loadFromCluster();
    this.k8sApi = this.kc.makeApiClient(k8s.CoreV1Api);
    this.customApi = this.kc.makeApiClient(k8s.CustomObjectsApi);
  }

  async getRabbitMQCluster() {
    try {
      const response = await this.customApi.getNamespacedCustomObject(
        'rabbitmq.com',
        'v1beta1',
        this.namespace,
        'rabbitmqclusters',
        this.rmqServiceName
      );
      return response.body;
    } catch (error) {
      throw new Error(`Failed to get RabbitMQ cluster: ${error.message}`);
    }
  }

  async updateRabbitMQResources(cpu, memory, dryRun = false) {
    if (dryRun) {
      console.log(`[DRY RUN] Would update resources to CPU: ${cpu}, Memory: ${memory}`);
      return;
    }

    const patch = {
      spec: {
        resources: {
          requests: { cpu, memory },
          limits: { cpu, memory }
        }
      }
    };

    try {
      await this.customApi.patchNamespacedCustomObject(
        'rabbitmq.com',
        'v1beta1',
        this.namespace,
        'rabbitmqclusters',
        this.rmqServiceName,
        patch,
        undefined,
        undefined,
        undefined,
        { headers: { 'Content-Type': 'application/merge-patch+json' } }
      );
    } catch (error) {
      throw new Error(`Failed to update RabbitMQ resources: ${error.message}`);
    }
  }

  async getConfigMap() {
    try {
      const response = await this.k8sApi.readNamespacedConfigMap(
        this.configMapName,
        this.namespace
      );
      return response.body;
    } catch (error) {
      if (error.statusCode === 404) {
        return null;
      }
      throw error;
    }
  }

  async updateConfigMap(data) {
    const configMapData = {
      apiVersion: 'v1',
      kind: 'ConfigMap',
      metadata: {
        name: this.configMapName,
        namespace: this.namespace
      },
      data
    };

    try {
      const existing = await this.getConfigMap();
      if (existing) {
        await this.k8sApi.patchNamespacedConfigMap(
          this.configMapName,
          this.namespace,
          { data },
          undefined,
          undefined,
          undefined,
          undefined,
          { headers: { 'Content-Type': 'application/merge-patch+json' } }
        );
      } else {
        await this.k8sApi.createNamespacedConfigMap(this.namespace, configMapData);
      }
    } catch (error) {
      throw new Error(`Failed to update ConfigMap: ${error.message}`);
    }
  }
}