import { ConfigProvider, Layout } from 'antd';

import { useTheme } from '@/hooks/useTheme';
import AppSidebar from '@/layouts/AppSidebar';
import CentralManagementTab from '@/pages/settings/CentralManagementTab';
import './CentralAccessPage.css';

export default function CentralAccessPage() {
  const { antdThemeConfig } = useTheme();

  return (
    <ConfigProvider theme={antdThemeConfig}>
      <Layout className="central-access-layout">
        <AppSidebar />
        <Layout.Content className="central-access-content">
          <section className="central-access-surface">
            <CentralManagementTab />
          </section>
        </Layout.Content>
      </Layout>
    </ConfigProvider>
  );
}
