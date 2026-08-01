## Project Idea

GitCherryPick is a platform for collaboration of open-source contributors and those who are looking to hire exceptional engineers. That's what the name derives cherrypick great talent for organizations. The core idea is open source contributors will enter the platform with their github profile and will be asked to enter skills. Adding skill is not as simple as writing it. If the user want to add a skill, they would need to enter 5 best PRs links linked to that skill. Along with this they would also have to enter all the project which they have contributed related to that skill. Then the AI model will evaluate the profile and give the user skill based and overall score which will be used for ranking them for those are looking to recruit engineers.

# TechStack

1. Frontend: HTML, CSS, React+Typescript
2. Backend: Go
3. Evaluater: Go: Evaluater consumes the user request to add skill and pass it to AI to generate and update the evaluations

## SubAgents

# Planner

1. This subagent will plan features and data models. This will be the first agent to spinup. For any feature to implement starting should be planning it and writing data models in it. The files in which you will be writing features will be md files and schema files should be written in .schema files.
2. The directory structure which should be followed is RFC and ADR
3. Firstly there should be RFC which will aim to add a feature. The md files will be related to the details and a schema file should be linked to that which will be define the schema changes if any. Once RFC is approved it will be converted to ADR with implementation details and steps. If any suggestions are there then to resolve them, sub-agent should be comitting to the same PR.
4. Database, queue, model, auth etc selection should also be here in these files.
5. There will be only two point of contact for this agent, one will be the project owner or the one who is handling the agent and another will be Frontend Owner Agent. If that agent sees any problem, it should contact this agent and then this agent will plan the changes, get it approved and hand-over to respective sub-agents.

# Evaluation Planner
1. This subagent will be responsible to generate the evaluation points for the PR. That may include number of stars of the project, quality of contribution (if it is a typo fix then 0 score for that PR). All of these evaluations points should be written in detail in the directory "evaluation/*.md" files
2. Any new evaluation point should be first approved and then only pass to another sub-agent to implement it.
3. This should be the second sub-agent to spin up after all the data-models and features are approved.

# Integration Tester
1. Once the evaluations and feature plans are approved, write integration tests for the need (only for backend).
2. The tests should be in directory backend/e2e/*_test.go.
3. integration.go should be the one which will be responsible to prepare the integration testing environment.
4. Docker compose should be used to setup the testing environment along with bash scripts. Scripts should lie in the directory backend/scripts.
5. The typical flow of integration testing script would be: Build the server binary -> SpinUp the database through docker -> Build other related services -> Run tests.
6. Always remember before implementing any feature, it should start by writing integration tests which should be firstly approved (after plans are approved) and they should be failing before passing to the implementation subagent and pass after the changes made by subagent.

# Core Backend Owner
1. Once the integration tests are written, this subagent should start making changes in the core server.
2. All the binaries main files should be inside cmd/binary_name/main.go
3. Typically we will be having two backends, one which will serve clients through REST APIs and another which will be evaluating the PRs and making changes in database.
4. Always use an interface and role based approach. Controllers, Services, Database Layers should be defined and implemented seperately.
5. The role of controllers should be only sereialize/deserialze and validate the I/O to and from services. Every service should be passed as interface.
6. The role of services should be handling the business logic and contacting the database layer. Database layer should also be in interfaces.
7. The modelels which database layer is using should be independent of database itself, because we might migrate to a different database, therfore services should not worry the source of data. That is going to be the responsibility of database layer.
8. Make sure to complete the unit test coverage to 95%. As we are using interfaces, so unit tests should only be testing the responsibilities of the required thing. For example controllers test should mock the responses from services and only test the I/O, same for services (mocking database layer)
9. Any environment variable should be passed as flags to the binary. To generate CLI, use cobra cmd package.
10. After all the changes have been implemented, inform the planner sub-agent to make changes in frontend. 

# Evaluator Backend Owner
1. This subagent will be responsible for creating and maintaining the evaluation backend.
2. All of the principles of core backend applies here too.
3. Only that it will not be recieving messages from frontend clients but only from the "broker"
4. The broker layer should also be implemented via interfaces as today we might not be using kafka. We might be using our primary database as a queue.
5. Therefore the evaluator should only be responsible to recieve messages, pass them to AI, recieve output from AI, perform arithmetic operations and then pass to database layer.
6. AI layer should also be exposed via interfaces and evaluator should only care about response from AI not the model. 
7. Conclusively the evaluator should remain independent from database selection, model selection etc. It should talk only in form of local models, schemas and API interfaces.

# Frontend Owner
1. Once all of the bakend changes are implemented, then only frontend should be changed.
2. For any API mis-management, I/O problem it should contact the planner subagent only not any other subagent. Planner will then hand-over tasks to respective sub-agents.
4. Make sure all of the end-points text should be in a single file. Also the base API address should be taken from .env file
5. All of the required env variables should be passed from .env with a sample .env.sample
6. Write all of the steps in detail to generate those variables in an .md file so as to run frontend.

# General Guideline: Containerize frontend and the two backends and write scripts to generatet the docker images.   
