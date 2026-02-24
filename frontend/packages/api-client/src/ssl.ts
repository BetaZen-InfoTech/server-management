import { apiClient } from "./client";
const PREFIX = "/api/v1/whm/ssl";
export const list = () => apiClient.get(PREFIX);
export const get = (domain: string) => apiClient.get(`${PREFIX}/${domain}`);
export const issueLetsEncrypt = (data: unknown) => apiClient.post(`${PREFIX}/letsencrypt`, data);
export const uploadCustom = (data: unknown) => apiClient.post(`${PREFIX}/custom`, data);
export const renew = (domain: string) => apiClient.post(`${PREFIX}/${domain}/renew`);
export const revoke = (domain: string) => apiClient.post(`${PREFIX}/${domain}/revoke`);
export const remove = (domain: string) => apiClient.delete(`${PREFIX}/${domain}`);
