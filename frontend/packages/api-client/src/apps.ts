import { apiClient } from "./client";

const PREFIX = "/api/v1/whm/apps";

export const list = (params?: Record<string, unknown>) => apiClient.get(PREFIX, { params });
export const get = (name: string) => apiClient.get(`${PREFIX}/${name}`);
export const deploy = (data: unknown) => apiClient.post(`${PREFIX}/deploy`, data);
export const redeploy = (name: string) => apiClient.post(`${PREFIX}/${name}/redeploy`);
export const start = (name: string) => apiClient.post(`${PREFIX}/${name}/start`);
export const stop = (name: string) => apiClient.post(`${PREFIX}/${name}/stop`);
export const restart = (name: string) => apiClient.post(`${PREFIX}/${name}/restart`);
export const remove = (name: string) => apiClient.delete(`${PREFIX}/${name}`);
export const logs = (name: string, lines?: number) => apiClient.get(`${PREFIX}/${name}/logs`, { params: { lines } });
export const updateEnv = (name: string, envVars: Record<string, string>, doRestart = true) =>
  apiClient.put(`${PREFIX}/${name}/env`, { env_vars: envVars, restart: doRestart });
export const rollback = (name: string, deploymentId?: string) =>
  apiClient.post(`${PREFIX}/${name}/rollback`, { deployment_id: deploymentId });
