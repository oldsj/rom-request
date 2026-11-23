import { useQueryClient } from '@tanstack/react-query';
import { Link, Outlet, useNavigate } from '@tanstack/react-router';

import { Button } from '@app/design';

import { $api, meQuery } from '../api/client';

const navLink = 'text-muted-foreground hover:text-foreground aria-[current=page]:text-foreground rounded-control px-1 py-2 aria-[current=page]:font-semibold focus-visible:outline-ring focus-visible:outline-2';

/** The signed-in frame: the app's name, navigation, and sign out. */
export function AppLayout() {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const logout = $api.useMutation('post', '/auth/logout', {
    onSuccess: () => {
      queryClient.setQueryData(meQuery.queryKey, null);
      queryClient.clear();
      void navigate({ to: '/login' });
    },
  });

  return (
    <>
      <header className="border-border mb-8 flex flex-wrap items-center justify-between gap-4 border-b pb-4">
        <p className="text-lg font-semibold">ROM Request</p>
        <nav aria-label="Main" className="flex items-center gap-4">
          <Link to="/" className={navLink} activeOptions={{ exact: true }}>Browse</Link>
          <Link to="/search" className={navLink}>Search</Link>
          <Link to="/requests" className={navLink}>Requests</Link>
          <Button onClick={() => { logout.mutate({}); }} disabled={logout.isPending}>Sign out</Button>
        </nav>
      </header>
      <Outlet />
    </>
  );
}
