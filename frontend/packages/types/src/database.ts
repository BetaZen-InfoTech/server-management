export interface Database {
  id: string;
  db_name: string;
  username: string;
  domain: string;
  host: string;
  port: number;
  connection_string: string;
  size_mb: number;
  created_at: string;
}

export interface CreateDatabaseRequest {
  db_name: string;
  username: string;
  password: string;
  domain: string;
}
