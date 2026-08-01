import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { ComponentType, CSSProperties } from 'react';
import { useLocation, useNavigate } from 'react-router';
import { useTranslation } from 'react-i18next';
import { Drawer, Layout, Menu } from 'antd';
import type { MenuProps } from 'antd';
import {
  CloseOutlined,
  CloudServerOutlined,
  ImportOutlined,
  LogoutOutlined,
  MenuOutlined,
  MoonFilled,
  MoonOutlined,
  SunOutlined,
  ToolOutlined,
} from '@ant-design/icons';

import { HttpUtil } from '@/utils';
import { pauseAnimationsUntilLeave, useTheme } from '@/hooks/useTheme';
import './AppSidebar.css';

const LOGOUT_KEY = '__logout__';
const RAIL_WIDTH = 72;
const railStyle = { '--sider-rail': `${RAIL_WIDTH}px` } as CSSProperties;

let hoveredAcrossRemounts = false;

type IconName = 'lines' | 'deploy' | 'diagnostics' | 'logout';

const iconByName: Record<IconName, ComponentType> = {
  lines: ImportOutlined,
  deploy: CloudServerOutlined,
  diagnostics: ToolOutlined,
  logout: LogoutOutlined,
};

function ThemeCycleButton({ id, isDark, isUltra, onCycle, ariaLabel }: {
  id: string;
  isDark: boolean;
  isUltra: boolean;
  onCycle: () => void;
  ariaLabel: string;
}) {
  const icon = !isDark ? <SunOutlined /> : !isUltra ? <MoonOutlined /> : <MoonFilled />;
  return (
    <button
      id={id}
      type="button"
      className="sidebar-theme-cycle"
      aria-label={ariaLabel}
      title={ariaLabel}
      onClick={onCycle}
    >
      {icon}
    </button>
  );
}

export default function AppSidebar() {
  const { t } = useTranslation();
  const { isDark, isUltra, toggleTheme, toggleUltra } = useTheme();
  const navigate = useNavigate();
  const { pathname } = useLocation();
  const [hovered, setHovered] = useState(() => hoveredAcrossRemounts);
  const [drawerOpen, setDrawerOpen] = useState(false);
  const railCollapsed = !hovered;
  const rootRef = useRef<HTMLDivElement>(null);

  const updateHovered = useCallback((value: boolean) => {
    hoveredAcrossRemounts = value;
    setHovered(value);
  }, []);

  useEffect(() => {
    const timer = window.setTimeout(() => {
      const el = rootRef.current;
      if (el) updateHovered(el.matches(':hover'));
    }, 150);
    return () => window.clearTimeout(timer);
  }, [updateHovered]);

  const currentTheme: 'light' | 'dark' = isDark ? 'dark' : 'light';
  const tabs = useMemo<{ key: string; icon: IconName; title: string }[]>(() => [
    { key: '/lines', icon: 'lines', title: '线路列表' },
    { key: '/lines/deploy', icon: 'deploy', title: '线路部署' },
    { key: '/diagnostics', icon: 'diagnostics', title: '诊断日志' },
    { key: LOGOUT_KEY, icon: 'logout', title: t('logout') },
  ], [t]);

  const navItems = useMemo(() => tabs.filter((tab) => tab.icon !== 'logout'), [tabs]);
  const utilItems = useMemo(() => tabs.filter((tab) => tab.icon === 'logout'), [tabs]);
  const lineDeployActive = pathname.startsWith('/lines/deploy');
  const lineListActive = pathname === '/' || pathname === '/lines' || (pathname.startsWith('/lines/') && !lineDeployActive);
  const selectedKey = lineDeployActive ? '/lines/deploy' : lineListActive ? '/lines' : pathname;
  const panelVersion = window.X_UI_CUR_VER || '?';

  const toMenuItems = useCallback((items: typeof tabs): MenuProps['items'] =>
    items.map((tab) => {
      const Icon = iconByName[tab.icon];
      return { key: tab.key, icon: <Icon />, label: tab.title, title: '' };
    }),
  []);

  const openLink = useCallback(async (key: string) => {
    if (key === LOGOUT_KEY) {
      await HttpUtil.post('/logout');
      window.location.href = window.X_UI_BASE_PATH || '/';
      return;
    }
    navigate(key);
  }, [navigate]);

  const onMenuClick = useCallback<NonNullable<MenuProps['onClick']>>(({ key }) => {
    openLink(String(key));
  }, [openLink]);

  const cycleTheme = useCallback((id: string) => {
    pauseAnimationsUntilLeave(id);
    if (!isDark) {
      toggleTheme();
      if (isUltra) toggleUltra();
    } else if (!isUltra) {
      toggleUltra();
    } else {
      toggleUltra();
      toggleTheme();
    }
  }, [isDark, isUltra, toggleTheme, toggleUltra]);

  return (
    <div
      ref={rootRef}
      className="ant-sidebar"
      style={railStyle}
      onMouseEnter={() => updateHovered(true)}
      onMouseLeave={() => updateHovered(false)}
    >
      <Layout.Sider
        theme={currentTheme}
        width={220}
        collapsedWidth={RAIL_WIDTH}
        collapsed={railCollapsed}
      >
        <div className="sider-brand">
          <div className="brand-block">
            <span className="brand-text">{railCollapsed ? 'RP' : 'Relay Panel'}</span>
          </div>
          {!railCollapsed && (
            <div className="brand-actions">
              <ThemeCycleButton
                id="theme-cycle"
                isDark={isDark}
                isUltra={isUltra}
                onCycle={() => cycleTheme('theme-cycle')}
                ariaLabel={t('menu.theme')}
              />
            </div>
          )}
        </div>
        <Menu
          theme={currentTheme}
          mode="inline"
          selectedKeys={[selectedKey]}
          className="sider-nav"
          items={toMenuItems(navItems)}
          onClick={onMenuClick}
        />
        <Menu
          theme={currentTheme}
          mode="inline"
          selectedKeys={[selectedKey]}
          className="sider-utility"
          items={toMenuItems(utilItems)}
          onClick={onMenuClick}
        />
        <div className="sider-footer">
          <span
            className={`sider-version${railCollapsed ? ' sider-version-collapsed' : ''}`}
            title={`Relay Panel v${panelVersion}`}
          >
            {railCollapsed ? `v${panelVersion}` : `Relay Panel v${panelVersion}`}
          </span>
        </div>
      </Layout.Sider>

      <Drawer
        placement="left"
        closable={false}
        open={drawerOpen}
        rootClassName={currentTheme}
        size="min(82vw, 320px)"
        styles={{
          wrapper: { padding: 0 },
          body: { padding: 0, display: 'flex', flexDirection: 'column', height: '100%' },
          header: { display: 'none' },
        }}
        onClose={() => setDrawerOpen(false)}
      >
        <div className="drawer-header">
          <div className="brand-block">
            <span className="drawer-brand">Relay Panel</span>
          </div>
          <div className="drawer-header-actions">
            <ThemeCycleButton
              id="theme-cycle-drawer"
              isDark={isDark}
              isUltra={isUltra}
              onCycle={() => cycleTheme('theme-cycle-drawer')}
              ariaLabel={t('menu.theme')}
            />
            <button
              className="drawer-close"
              type="button"
              aria-label={t('close')}
              onClick={() => setDrawerOpen(false)}
            >
              <CloseOutlined />
            </button>
          </div>
        </div>
        <Menu
          theme={currentTheme}
          mode="inline"
          selectedKeys={[selectedKey]}
          className="drawer-menu drawer-nav"
          items={toMenuItems(navItems)}
          onClick={(info) => { onMenuClick(info); setDrawerOpen(false); }}
        />
        <Menu
          theme={currentTheme}
          mode="inline"
          selectedKeys={[selectedKey]}
          className="drawer-menu drawer-utility"
          items={toMenuItems(utilItems)}
          onClick={(info) => { onMenuClick(info); setDrawerOpen(false); }}
        />
        <div className="drawer-footer">
          <span className="sider-version">Relay Panel v{panelVersion}</span>
        </div>
      </Drawer>

      {!drawerOpen && (
        <button
          className="drawer-handle"
          type="button"
          aria-label={t('menu.openMenu')}
          onClick={() => setDrawerOpen(true)}
        >
          <MenuOutlined />
        </button>
      )}
    </div>
  );
}
