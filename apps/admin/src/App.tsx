import { useEffect, useState } from 'react';
import { Routes, Route, NavLink, Navigate, useLocation, useNavigate } from 'react-router-dom';
import { api, setActiveStore, SessionInfo } from './api/client';
import Login from './pages/Login';
import Board from './pages/Board';
import Categories from './pages/Categories';
import Dishes from './pages/Dishes';
import Promotions from './pages/Promotions';
import Tables from './pages/Tables';
import Analytics from './pages/Analytics';
import Materials from './pages/Materials';
import Members from './pages/Members';
import StoreExport from './pages/StoreExport';

// 角色层级（PRD §3）：STAFF < MANAGER < OWNER。
const ROLE_RANK: Record<string, number> = { STAFF: 1, MANAGER: 2, OWNER: 3 };
function atLeast(role: string | undefined, min: string): boolean {
  return (ROLE_RANK[role ?? ''] ?? 0) >= (ROLE_RANK[min] ?? 0);
}

export default function App() {
  const [session, setSession] = useState<SessionInfo | null>(null);
  const [loading, setLoading] = useState(true);
  const loc = useLocation();
  const nav = useNavigate();

  useEffect(() => {
    if (loc.pathname === '/login') {
      setLoading(false);
      return;
    }
    api
      .get<SessionInfo>('/api/v1/admin/session')
      .then((s) => {
        setSession(s);
        if (s.active_store_id) setActiveStore(s.active_store_id);
      })
      .catch(() => {
        setSession(null);
        nav('/login');
      })
      .finally(() => setLoading(false));
  }, [loc.pathname, nav]);

  if (loading) return <div className="main">加载中…</div>;
  if (!session) {
    return (
      <Routes>
        <Route path="/login" element={<Login onLogin={setSession} />} />
        <Route path="*" element={<Navigate to="/login" replace />} />
      </Routes>
    );
  }

  const hasStore = !!session.active_store_id;

  return (
    <div className="layout">
      <nav className="sidebar">
        <div className="brand">DishFlow</div>
        {hasStore && (
          <>
            <NavLink to="/board" className={({ isActive }) => (isActive ? 'active' : '')}>订单工作台</NavLink>
            <NavLink to="/categories">分类</NavLink>
            <NavLink to="/dishes">菜品/库存</NavLink>
            <NavLink to="/promotions">满减/券</NavLink>
            <NavLink to="/tables">桌台</NavLink>
            <NavLink to="/members">会员</NavLink>
            <NavLink to="/materials">物料</NavLink>
            <NavLink to="/analytics">经营分析</NavLink>
            <NavLink to="/export">备份导入导出</NavLink>
          </>
        )}
        <NavLink to="/login">退出登录</NavLink>
        {session.is_platform_admin && <div className="muted" style={{ padding: '10px 20px' }}>平台超管</div>}
      </nav>
      <main className="main">
        {!hasStore && <div className="error-box">当前账号未绑定门店。请通过“开店与加入”获得门店成员关系。</div>}
        <div className="topbar">
          <div>
            {session.display_name || session.login}
            {session.role && <span className="tag" style={{ marginLeft: 8 }}>{session.role}</span>}
          </div>
          <button
            onClick={async () => {
              await api.del('/api/v1/admin/session');
              setSession(null);
              nav('/login');
            }}
          >
            退出
          </button>
        </div>
        <Routes>
          <Route path="/" element={<Navigate to={hasStore ? '/board' : '/login'} replace />} />
          <Route path="/board" element={hasStore ? <Board role={session.role} /> : <NoStore />} />
          <Route path="/categories" element={hasStore ? <Categories role={session.role} /> : <NoStore />} />
          <Route path="/dishes" element={hasStore ? <Dishes role={session.role} /> : <NoStore />} />
          <Route path="/promotions" element={hasStore ? <Promotions role={session.role} /> : <NoStore />} />
          <Route path="/tables" element={hasStore ? <Tables role={session.role} /> : <NoStore />} />
          <Route path="/members" element={hasStore ? <Members role={session.role} /> : <NoStore />} />
          <Route path="/materials" element={hasStore ? <Materials role={session.role} /> : <NoStore />} />
          <Route path="/analytics" element={hasStore && atLeast(session.role, 'MANAGER') ? <Analytics /> : <NoStore />} />
          <Route path="/export" element={hasStore && atLeast(session.role, 'MANAGER') ? <StoreExport role={session.role} /> : <NoStore />} />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </main>
    </div>
  );
}

function NoStore() {
  return <div className="muted">请选择或绑定门店后访问。</div>;
}
