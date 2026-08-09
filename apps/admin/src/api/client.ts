// DishFlow 管理后台 API 客户端。
// 金额一律 int64 分；时间 RFC3339；错误统一 {code,message,request_id,details}（PRD §16）。

export interface ApiError {
  code: string;
  message: string;
  request_id: string;
  details?: unknown;
}

export interface Page<T> {
  items: T[];
  next_cursor?: string;
  total?: number;
}

// 分转换为元字符串（PRD §4.5：UI 只负责格式化）。
export function centsToYuan(cents: number): string {
  const neg = cents < 0;
  const abs = Math.abs(cents);
  const yuan = Math.floor(abs / 100);
  const fen = abs % 100;
  const s = `${yuan}.${fen.toString().padStart(2, '0')}`;
  return neg ? `-${s}` : s;
}

async function request<T>(method: string, path: string, body?: unknown, storeId?: number): Promise<T> {
  const headers: Record<string, string> = {};
  let payload: BodyInit | undefined;
  if (body !== undefined) {
    headers['Content-Type'] = 'application/json';
    payload = JSON.stringify(body);
  }
  if (storeId !== undefined && storeId > 0) {
    headers['X-Store-Id'] = String(storeId);
  }
  const resp = await fetch(path, {
    method,
    headers,
    credentials: 'include', // 携带 shop_session Cookie（PRD §5.1）
    body: payload,
  });
  const text = await resp.text();
  const data = text ? JSON.parse(text) : null;
  if (!resp.ok) {
    throw data as ApiError;
  }
  return data as T;
}

// 当前门店上下文（普通账号仅一个门店，从 /admin/session 读得）。
let activeStoreId = 0;
export function setActiveStore(id: number): void {
  activeStoreId = id;
}
export function getActiveStore(): number {
  return activeStoreId;
}

export const api = {
  get: <T>(path: string) => request<T>('GET', path, undefined, getActiveStore()),
  post: <T>(path: string, body?: unknown) => request<T>('POST', path, body, getActiveStore()),
  patch: <T>(path: string, body?: unknown) => request<T>('PATCH', path, body, getActiveStore()),
  del: <T>(path: string) => request<T>('DELETE', path, undefined, getActiveStore()),
};

// ── 类型定义（与 Go DTO 对齐）─────────────────────────────────────────

export interface SessionInfo {
  id: number;
  login: string;
  display_name: string;
  is_platform_admin: boolean;
  active_store_id?: number;
  role?: string;
}

export interface Order {
  id: number;
  order_no: string;
  pickup_no?: number;
  scenario: string;
  table_label?: string;
  pickup_type: string;
  scheduled_for?: string;
  payable_cents: number;
  fulfillment_state: string;
  version: number;
  created_at: string;
  paid_at?: string;
}

export interface OrderDetail extends Order {
  items: Record<string, unknown>[];
  events: Record<string, unknown>[];
}

export interface BoardColumn {
  state: string;
  orders: Order[];
}

export interface Category {
  id: number;
  name: string;
  enabled: boolean;
  sort_order: number;
  deleted_at?: string;
  delete_batch_id?: string;
}

export interface Overview {
  pay_amount_cents: number;
  refund_amount_cents: number;
  net_amount_cents: number;
  pay_order_count: number;
  refund_order_count: number;
  avg_order_cents: number;
  customer_count: number;
  new_customer_count: number;
}
