import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { api, ApiError, SessionInfo, setActiveStore } from '../api/client';

export default function Login({ onLogin }: { onLogin: (s: SessionInfo) => void }) {
  const [login, setLogin] = useState('');
  const [password, setPassword] = useState('');
  const [err, setErr] = useState('');
  const nav = useNavigate();

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setErr('');
    try {
      await api.post('/api/v1/admin/session', { login, password });
      // 登录成功后立即 GET session 拿完整信息（含 active_store_id/role）。
      const s = await api.get<SessionInfo>('/api/v1/admin/session');
      if (s.active_store_id) setActiveStore(s.active_store_id);
      onLogin(s);
      nav(s.active_store_id ? '/board' : '/');
    } catch (e) {
      const apiErr = e as ApiError;
      setErr(apiErr.code === 'RATE_LIMITED' ? '尝试过于频繁，请稍后再试' : '账号或密码错误');
    }
  }

  return (
    <div style={{ maxWidth: 360, margin: '80px auto' }}>
      <h1 style={{ textAlign: 'center' }}>DishFlow 管理后台</h1>
      <form className="card col" onSubmit={submit}>
        {err && <div className="error-box">{err}</div>}
        <input
          placeholder="登录账号"
          value={login}
          onChange={(e) => setLogin(e.target.value)}
          autoComplete="username"
        />
        <input
          type="password"
          placeholder="密码"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          autoComplete="current-password"
        />
        <button className="primary" type="submit">
          登录
        </button>
      </form>
    </div>
  );
}
