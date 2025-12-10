#!/usr/bin/env python3
import os
import subprocess


url = "https://github.com/apache/datafusion"
name = url.split("/")[-1]   # we get "datafusion" or the get repo name by splitting the string 

def process(repo_url):
    # Clone if missing, otherwise pull
    if not os.path.isdir(name):
        print(f"Cloning {repo_url}")
        subprocess.run(["git", "clone", repo_url], check=True)
    else:
        print(f"Repo {name} already exists, pulling latest changes...")
        subprocess.run(["git", "-C", name, "pull"], check=True)




    # 2. Walk through repo and find Rust files
    matches = []
    for root, _, files in os.walk(name):
        for file in files:
            if file.endswith(".rs"):
                file_path = os.path.join(root, file)

                # check for ab is it is inside the file
                with open(file_path, "r", errors="ignore") as f:
                    content = f.read()
                    if "ab" in content:
                        matches.append(file_path)


                        

    return matches





if __name__ == "__main__":
    # Run process and save results
    matches = process(url)

    with open("output.txt", "w") as out:
        out.write("\n".join(matches))

