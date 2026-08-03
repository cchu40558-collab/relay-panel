import { lazy, Suspense } from 'react';
import { Navigate, createBrowserRouter, type RouteObject } from 'react-router';

import PanelLayout from '@/layouts/PanelLayout';

const LinesPage = lazy(() => import('@/pages/lines/LinesPage'));

function withSuspense(node: React.ReactNode) {
  return <Suspense fallback={null}>{node}</Suspense>;
}

const routes: RouteObject[] = [
  {
    path: '/',
    element: <PanelLayout />,
    children: [
      { index: true, element: withSuspense(<LinesPage />) },
      { path: 'lines', element: withSuspense(<LinesPage />) },
      { path: 'lines/:id', element: withSuspense(<LinesPage />) },
      { path: 'lines/:id/edit', element: withSuspense(<LinesPage />) },
      { path: 'lines/deploy', element: withSuspense(<LinesPage />) },
      { path: 'lines/deploy/cloudflare', element: withSuspense(<LinesPage />) },
		{ path: 'lines/deploy/bunny', element: withSuspense(<LinesPage />) },
      { path: 'lines/deploy/reality', element: withSuspense(<LinesPage />) },
      { path: 'diagnostics', element: withSuspense(<LinesPage />) },
      { path: '*', element: <Navigate to="/lines" replace /> },
    ],
  },
];

function computeBasename() {
  const raw = (typeof window !== 'undefined' && window.X_UI_BASE_PATH) || '/';
  const trimmed = raw.replace(/\/+$/, '');
  return `${trimmed}/panel`;
}

export const router = createBrowserRouter(routes, {
  basename: computeBasename(),
});
