"use client";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { getErrorMessage, useIsAuthEnabledQuery, useLoginMutation } from "@/lib/store";
import { BooksIcon, DiscordLogoIcon, GithubLogoIcon } from "@phosphor-icons/react";
import { Eye, EyeOff } from "lucide-react";
import Image from "next/image";
import { useRouter } from "next/navigation";
import { useEffect, useState } from "react";
import { useTheme } from "next-themes";

const externalLinks = [
	{
		title: "Discord Server",
		url: "https://discord.gg/exN5KAydbU",
		icon: DiscordLogoIcon,
	},
	{
		title: "GitHub Repository",
		url: "https://github.com/maximhq/bifrost",
		icon: GithubLogoIcon,
	},
	{
		title: "Full Documentation",
		url: "https://docs.getbifrost.ai",
		icon: BooksIcon,
		strokeWidth: 1,
	},
];

export default function LoginView() {
	const { resolvedTheme } = useTheme();
	const [mounted, setMounted] = useState(false);
	const [username, setUsername] = useState("");
	const [password, setPassword] = useState("");
	const [showPassword, setShowPassword] = useState(false);
	const [errorMessage, setErrorMessage] = useState("");
	const [isCheckingAuth, setIsCheckingAuth] = useState(true);
	const [isLoading, setIsLoading] = useState(false);
	const router = useRouter();
	const { data: isAuthEnabledData, isLoading: isLoadingIsAuthEnabled, error: isAuthEnabledError } = useIsAuthEnabledQuery();
	const [login, { isLoading: isLoggingIn }] = useLoginMutation();

	const isAuthEnabled = isAuthEnabledData?.is_auth_enabled || false;
	const hasValidToken = isAuthEnabledData?.has_valid_token || false;
	const ssoEnabled = isAuthEnabledData?.sso_enabled || false;

	useEffect(() => {
		setMounted(true);
	}, []);

	useEffect(() => {
		if (isLoadingIsAuthEnabled) {
			return;
		}
		if (isAuthEnabledError) {
			setErrorMessage("Unable to verify authentication status. Please retry.");
			setIsCheckingAuth(false);
			return;
		}
		if (!isAuthEnabled || hasValidToken) {
			router.push("/workspace");
			return;
		}
		setIsCheckingAuth(false);
	}, [hasValidToken, isAuthEnabled, isAuthEnabledError, isLoadingIsAuthEnabled, router]);

	const handleSubmit = async (e: React.FormEvent<HTMLFormElement>) => {
		e.preventDefault();
		setIsLoading(true);
		setErrorMessage("");
		try {
			await login({ username, password }).unwrap();
			router.push("/workspace");
		} catch (error) {
			setErrorMessage(getErrorMessage(error));
		} finally {
			setIsLoading(false);
		}
	};

	const handleSsoLogin = () => {
		window.location.href = "/api/sso/login";
	};

	const logoSrc = mounted && resolvedTheme === "dark" ? "/bifrost-logo-dark.png" : "/bifrost-logo.png";

	if (isCheckingAuth || isLoadingIsAuthEnabled) {
		return (
			<div className="flex min-h-screen items-center justify-center p-4">
				<div className="w-full max-w-md">
					<div className="border-border bg-card w-full space-y-6 rounded-sm border p-8">
						<div className="flex items-center justify-center">
							<Image src={logoSrc} alt="Bifrost" width={160} height={26} priority />
						</div>
						<div className="flex items-center justify-center py-8">
							<div className="text-muted-foreground text-sm">Checking authentication...</div>
						</div>
					</div>
				</div>
			</div>
		);
	}

	return (
		<div className="flex min-h-screen items-center justify-center p-4">
			<div className="w-full max-w-md">
				<div className="border-border bg-card w-full space-y-6 rounded-sm border p-8">
					<div className="flex items-center justify-center">
						<Image src={logoSrc} alt="Bifrost" width={160} height={26} priority />
					</div>

					<div className="space-y-2 text-center">
						<h1 className="text-foreground text-lg font-semibold">Welcome back</h1>
						<p className="text-muted-foreground text-sm">Sign in to your account to continue</p>
					</div>

					<form onSubmit={handleSubmit} className="space-y-5">
						{errorMessage && <div className="bg-destructive/10 text-destructive rounded-sm p-3 text-sm">{errorMessage}</div>}

						<div className="space-y-2">
							<Label htmlFor="username" className="text-sm font-medium">
								Username
							</Label>
							<Input
								id="username"
								type="text"
								placeholder="Enter your username"
								value={username}
								onChange={(e) => setUsername(e.target.value)}
								required
								className="text-sm"
								autoComplete="username"
								data-testid="login-username-input"
							/>
						</div>

						<div className="space-y-2">
							<Label htmlFor="password" className="text-sm font-medium">
								Password
							</Label>
							<div className="relative">
								<Input
									id="password"
									type={showPassword ? "text" : "password"}
									placeholder="Enter your password"
									value={password}
									onChange={(e) => setPassword(e.target.value)}
									required
									className="pr-10 text-sm"
									autoComplete="current-password"
									data-testid="login-password-input"
								/>
								<button
									type="button"
									onClick={() => setShowPassword(!showPassword)}
									className="text-muted-foreground hover:text-foreground absolute right-3 top-1/2 -translate-y-1/2 transition-colors"
									aria-label={showPassword ? "Hide password" : "Show password"}
								>
									{showPassword ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
								</button>
							</div>
						</div>

						<Button type="submit" className="h-9 w-full text-sm" isLoading={isLoading} disabled={isLoading} dataTestId="login-submit-btn">
							{isLoading || isLoggingIn ? "Signing in..." : "Sign in"}
						</Button>

						{ssoEnabled && (
							<>
								<div className="flex items-center gap-3 py-1">
									<div className="bg-border h-px flex-1" />
									<span className="text-muted-foreground text-xs uppercase tracking-[0.2em]">or</span>
									<div className="bg-border h-px flex-1" />
								</div>
								<Button
									type="button"
									variant="outline"
									className="h-9 w-full text-sm"
									onClick={handleSsoLogin}
									dataTestId="login-sso-btn"
								>
									Sign in with SSO
								</Button>
							</>
						)}
					</form>

					<div className="flex items-center justify-center gap-4 pt-4">
						{externalLinks.map((item, index) => (
							<a
								key={index}
								href={item.url}
								target="_blank"
								rel="noopener noreferrer"
								className="text-muted-foreground hover:text-primary transition-colors"
								title={item.title}
							>
								<item.icon className="h-5 w-5" size={20} weight="regular" strokeWidth={item.strokeWidth} />
							</a>
						))}
					</div>
				</div>
			</div>
		</div>
	);
}
