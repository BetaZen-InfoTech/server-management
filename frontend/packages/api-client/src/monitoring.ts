import { apiClient } from "./client";
const PREFIX = "/api/v1/whm/monitor";
export const systemInfo = () => apiClient.get(`${PREFIX}/system`);
export const metrics = () => apiClient.get(`${PREFIX}/metrics`);
export const services = () => apiClient.get(`${PREFIX}/services`);
export const history = (metric: string, period: string, interval: string) =>
  apiClient.get(`${PREFIX}/history`, { params: { metric, period, interval } });
export const getAlerts = () => apiClient.get(`${PREFIX}/alerts`);
export const updateAlerts = (data: unknown) => apiClient.put(`${PREFIX}/alerts`, data);
