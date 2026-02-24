import { apiClient } from "./client";

const PREFIX = "/api/v1/whm/domains";

export const list = (params?: Record<string, unknown>) => apiClient.get(PREFIX, { params });
export const get = (id: string) => apiClient.get(`${PREFIX}/${id}`);
export const create = (data: unknown) => apiClient.post(PREFIX, data);
export const update = (id: string, data: unknown) => apiClient.put(`${PREFIX}/${id}`, data);
export const remove = (id: string) => apiClient.delete(`${PREFIX}/${id}`, { data: { confirm: true } });
export const suspend = (id: string) => apiClient.patch(`${PREFIX}/${id}/suspend`);
export const unsuspend = (id: string) => apiClient.patch(`${PREFIX}/${id}/unsuspend`);
export const switchPHP = (id: string, version: string) => apiClient.patch(`${PREFIX}/${id}/php`, { php_version: version });
export const stats = (id: string) => apiClient.get(`${PREFIX}/${id}/stats`);
