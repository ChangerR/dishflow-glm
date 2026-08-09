// utils/api.ts — 小程序请求封装。
// 鉴权：X-Wechat-Appid（门店定位）+ Bearer token（顾客会话，PRD §2.2/§4.1）。

const app = getApp();

interface ApiError {
  code: string;
  message: string;
  request_id: string;
}

export function centsToYuan(cents: number): string {
  const neg = cents < 0;
  const abs = Math.abs(cents);
  return (neg ? '-' : '') + Math.floor(abs / 100) + '.' + (abs % 100).toString().padStart(2, '0');
}

// 统一请求：业务对象或抛出 ApiError。
export function request<T>(method: 'GET' | 'POST' | 'DELETE', path: string, data?: unknown): Promise<T> {
  const header: Record<string, string> = {
    'X-Wechat-Appid': app.globalData.appid,
  };
  if (app.globalData.token) {
    header['Authorization'] = 'Bearer ' + app.globalData.token;
  }
  if (data !== undefined && method !== 'GET') {
    header['Content-Type'] = 'application/json';
  }
  return new Promise<T>((resolve, reject) => {
    wx.request({
      url: 'http://172.17.186.176:8090' + path, // WSL 局域网 IP + 端口
      method,
      data: data as string | WechatMiniprogram.IAnyObject | undefined,
      header,
      success: (res) => {
        const body = res.data as T | ApiError;
        if (res.statusCode >= 200 && res.statusCode < 300) {
          resolve(body as T);
        } else {
          // 401 统一回登录（PRD §5.1）；顾客侧落到 wx.login。
          reject(body as ApiError);
        }
      },
      fail: (err) => reject({ code: 'NETWORK', message: err.errMsg, request_id: '' } as ApiError),
    });
  });
}

// wx.login 换顾客会话（PRD §4.1.5）。
export function ensureSession(): Promise<void> {
  if (app.globalData.token) return Promise.resolve();
  return new Promise((resolve, reject) => {
    wx.login({
      success: (res) => {
        request<{ token: string; expires_at: string }>('POST', '/api/v1/auth/wechat/session', { code: res.code })
          .then((r) => {
            app.globalData.token = r.token;
            wx.setStorageSync('token', r.token);
            resolve();
          })
          .catch(reject);
      },
      fail: () => reject({ code: 'WX_LOGIN_FAILED', message: 'wx.login 失败', request_id: '' } as ApiError),
    });
  });
}
