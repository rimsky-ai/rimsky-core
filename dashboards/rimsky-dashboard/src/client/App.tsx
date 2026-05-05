// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { Routes, Route } from 'react-router-dom';
import { Layout } from './components/Layout';

import SystemPage from './routes/SystemPage';
import TemplatesPage from './routes/TemplatesPage';
import TemplateDetailPage from './routes/TemplateDetailPage';
import InstancesPage from './routes/InstancesPage';
import InstanceDetailPage from './routes/InstanceDetailPage';
import FramesPage from './routes/FramesPage';
import FrameDetailPage from './routes/FrameDetailPage';
import NodeDetailPage from './routes/NodeDetailPage';
import DispatchesPage from './routes/DispatchesPage';
import DispatchDetailPage from './routes/DispatchDetailPage';
import LockHoldersPage from './routes/LockHoldersPage';
import LockHolderDetailPage from './routes/LockHolderDetailPage';
import SchedulesPage from './routes/SchedulesPage';
import EventsPage from './routes/EventsPage';
import StoresPage from './routes/StoresPage';
import StoreDetailPage from './routes/StoreDetailPage';
import ExecutorsPage from './routes/ExecutorsPage';
import ExecutorDetailPage from './routes/ExecutorDetailPage';

export default function App() {
  return (
    <Layout>
      <Routes>
        <Route path="/" element={<SystemPage />} />
        <Route path="/templates" element={<TemplatesPage />} />
        <Route path="/templates/:hash" element={<TemplateDetailPage />} />
        <Route path="/instances" element={<InstancesPage />} />
        <Route path="/instances/:id" element={<InstanceDetailPage />} />
        <Route path="/instances/:instanceId/nodes/:nodeType" element={<NodeDetailPage />} />
        <Route path="/frames" element={<FramesPage />} />
        <Route path="/frames/:id" element={<FrameDetailPage />} />
        <Route path="/dispatches" element={<DispatchesPage />} />
        <Route path="/dispatches/:id" element={<DispatchDetailPage />} />
        <Route path="/lock-holders" element={<LockHoldersPage />} />
        <Route path="/lock-holders/:id" element={<LockHolderDetailPage />} />
        <Route path="/schedules" element={<SchedulesPage />} />
        <Route path="/events" element={<EventsPage />} />
        <Route path="/stores" element={<StoresPage />} />
        <Route path="/stores/:name" element={<StoreDetailPage />} />
        <Route path="/executors" element={<ExecutorsPage />} />
        <Route path="/executors/:name" element={<ExecutorDetailPage />} />
        <Route path="*" element={<SystemPage />} />
      </Routes>
    </Layout>
  );
}
