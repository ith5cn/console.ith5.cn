import { useState } from 'react'
import { clearAuth,getToken,getUser,type UserInfo } from '@/api'
import { AppShell } from '@/components/AppShell'
import { Toaster } from '@/components/ui/toaster'
import type { Page } from '@/navigation'
import { Activate } from '@/pages/Activate'; import { Assignments } from '@/pages/Assignments'; import { Audit } from '@/pages/Audit'; import { Bundles } from '@/pages/Bundles'; import { Explain } from '@/pages/Explain'; import { Groups } from '@/pages/Groups'; import { Login } from '@/pages/Login'; import { Members } from '@/pages/Members'; import { Overview } from '@/pages/Overview'
export function App(){const [user,setUser]=useState<UserInfo|null>(getToken()?getUser():null);const [page,setPage]=useState<Page>('overview');if(location.pathname==='/activate')return <><Activate/><Toaster/></>;if(!user)return <><Login onDone={setUser}/><Toaster/></>;return <AppShell user={user} page={page} onPageChange={setPage} onLogout={()=>{clearAuth();setUser(null)}}><>{page==='overview'&&<Overview/>}{page==='bundles'&&<Bundles/>}{page==='groups'&&<Groups/>}{page==='assignments'&&<Assignments/>}{page==='members'&&<Members/>}{page==='audit'&&<Audit/>}{page==='explain'&&<Explain/>}<Toaster/></></AppShell>}
