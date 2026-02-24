import { apiClient } from "./client";
const PREFIX = "/api/v1/whm/databases";
export const list = (params?: Record<string, unknown>) => apiClient.get(PREFIX, { params });
export const get = (id: string) => apiClient.get(`${PREFIX}/${id}`);
export const create = (data: unknown) => apiClient.post(PREFIX, data);
export const remove = (id: string) => apiClient.delete(`${PREFIX}/${id}`, { data: { confirm: true } });
export const listUsers = (id: string) => apiClient.get(`${PREFIX}/${id}/users`);
export const createUser = (id: string, data: unknown) => apiClient.post(`${PREFIX}/${id}/users`, data);
export const deleteUser = (id: string, userId: string) => apiClient.delete(`${PREFIX}/${id}/users/${userId}`);
