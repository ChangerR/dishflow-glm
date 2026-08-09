import { useEffect, useState } from 'react';
import { api, ApiError, Overview, centsToYuan } from '../api/client';

export default function Analytics() {
  const [ov, setOv] = useState<Overview | null>(null);
  const [err, setErr] = useState('');

  useEffect(() => {
    api
      .get<Overview>('/api/v1/admin/analytics/overview')
      .then(setOv)
      .catch((e) => setErr((e as ApiError).message));
  }, []);

  if (err) return <div className="error-box">{err}</div>;
  if (!ov) return <div>加载中…</div>;

  return (
    <div>
      <h2>经营分析</h2>
      <div className="grid4">
        <div className="card">
          <div className="muted">支付金额</div>
          <div style={{ fontSize: 22, fontWeight: 600 }}>{centsToYuan(ov.pay_amount_cents)}</div>
        </div>
        <div className="card">
          <div className="muted">退款金额</div>
          <div style={{ fontSize: 22, fontWeight: 600, color: '#ff4d4f' }}>{centsToYuan(ov.refund_amount_cents)}</div>
        </div>
        <div className="card">
          <div className="muted">净收入</div>
          <div style={{ fontSize: 22, fontWeight: 600 }}>{centsToYuan(ov.net_amount_cents)}</div>
        </div>
        <div className="card">
          <div className="muted">支付订单数</div>
          <div style={{ fontSize: 22, fontWeight: 600 }}>{ov.pay_order_count}</div>
        </div>
      </div>
      <div className="card">
        <table>
          <tbody>
            <tr>
              <td className="muted">客单价</td>
              <td>{centsToYuan(ov.avg_order_cents)}</td>
            </tr>
            <tr>
              <td className="muted">客户数</td>
              <td>{ov.customer_count}</td>
            </tr>
            <tr>
              <td className="muted">新客</td>
              <td>{ov.new_customer_count}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>
  );
}
